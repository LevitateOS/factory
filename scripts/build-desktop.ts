import {buildDesktopUI} from './build-desktop-ui.js';
import {isAbsolute, relative, resolve, sep} from 'node:path';
import {cp, copyFile, mkdir} from 'node:fs/promises';
import {parseArgs} from 'node:util';
const root = resolve(import.meta.dir, '..');
const {values} = parseArgs({
  args: Bun.argv.slice(2),
  options: {postgres: {type: 'string'}, out: {type: 'string'}, check: {type: 'boolean'}},
});
if (!values.postgres || !values.out) throw Error('Explicit --postgres native payload and --out stage are required');
const payload = resolve(values.postgres),
  out = resolve(values.out);
function contains(parent: string, child: string): boolean {
  const path = relative(parent, child);
  return path === '' || (path !== '..' && !path.startsWith('..' + sep) && !isAbsolute(path));
}
if (contains(payload, out) || contains(out, payload)) throw Error('Output must be separate from the native payload');
await buildDesktopUI(resolve(root, '.artifacts/desktop-ui'));
await mkdir(resolve(out, 'factory-runtime'), {recursive: true});
async function run(args: string[]) {
  const child = Bun.spawn(args, {cwd: root, stdout: 'inherit', stderr: 'inherit'});
  if ((await child.exited) !== 0) throw Error('Native desktop build failed');
}
await run([
  'go',
  'build',
  '-mod=readonly',
  '-o',
  resolve(out, 'factory-runtime/soda-factory-server'),
  './cmd/soda-factory-server',
]);
const desktop = Bun.spawn(
  [
    'cargo',
    'build',
    '--locked',
    '-p',
    'soda-desktop',
    '-p',
    'soda-factory-worker',
    '--bin',
    'soda-desktop',
    '--bin',
    'soda-factory-muse',
    '--features',
    values.check ? 'soda-desktop/native-check' : 'soda-desktop/native-ui',
    '--message-format=json-render-diagnostics',
  ],
  {cwd: root, stdout: 'pipe', stderr: 'inherit'}
);
const [status, messages] = await Promise.all([desktop.exited, new Response(desktop.stdout).text()]);
const executables = new Map<string, string>();
for (const line of messages.split('\n')) {
  if (!line) continue;
  const message = JSON.parse(line);
  if (message.reason === 'compiler-message' && message.message.rendered) process.stderr.write(message.message.rendered);
  if (message.reason === 'compiler-artifact' && message.executable)
    executables.set(message.target.name, message.executable);
}
if (status !== 0) throw Error('Native desktop build failed');
for (const name of ['soda-desktop', 'soda-factory-muse']) {
  const executable = executables.get(name);
  if (!executable) throw Error('Native desktop build did not produce ' + name);
  await copyFile(executable, resolve(out, name));
}
await cp(payload, resolve(out, 'factory-runtime/postgresql'), {recursive: true, verbatimSymlinks: true});
console.log('Staged standalone desktop at ' + out);

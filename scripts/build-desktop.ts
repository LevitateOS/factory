import {buildDesktopUI} from './build-desktop-ui.js';
import {resolve} from 'node:path';
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
if (out === payload || out.startsWith(payload + '/')) throw Error('Output must be separate from the native payload');
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
  '-tags=factory_native',
  '-o',
  resolve(out, 'factory-runtime/soda-factory-server'),
  './cmd/soda-factory-server',
]);
await run([
  'cargo',
  'build',
  '--locked',
  '-p',
  'soda-desktop',
  '--features',
  values.check ? 'native-check' : 'native-ui',
]);
await copyFile(resolve(root, 'target/debug/soda-desktop'), resolve(out, 'soda-desktop'));
await cp(payload, resolve(out, 'factory-runtime/postgresql'), {recursive: true, verbatimSymlinks: true});
console.log('Staged standalone desktop at ' + out);

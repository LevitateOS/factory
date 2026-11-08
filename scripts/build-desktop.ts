import {buildDesktopUI} from './build-desktop-ui.js';
import {isAbsolute, relative, resolve, sep} from 'node:path';
import {chmod, cp, copyFile, lstat, mkdir, readdir, writeFile} from 'node:fs/promises';
import {parseArgs} from 'node:util';
import {stageDesktopInstaller} from './stage-desktop-installer.js';
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
if (await lstat(out).then(() => true, error => {if (error.code === 'ENOENT') return false; throw error;}))
  throw Error('Use a new output directory; retained stages are never overwritten');
if (!values.check && !(await Bun.file(resolve(payload, 'COPYRIGHT')).exists()))
  throw Error('Native PostgreSQL payload must retain its source COPYRIGHT notice');
if (values.check && await Bun.file(resolve(out, 'install.sh')).exists())
  throw Error('Check builds require a separate stage without an app installer');
// Packaging selects a new payload; it never upgrades an existing profile here.
const versionsResponse = await fetch('https://www.postgresql.org/versions.json', {signal: AbortSignal.timeout(15_000)});
if (!versionsResponse.ok) throw Error('Official PostgreSQL release metadata unavailable');
const versions: unknown = await versionsResponse.json();
if (!Array.isArray(versions)) throw Error('Invalid PostgreSQL release metadata');
const current = versions.filter(version => version?.current === true && version.supported === true);
if (current.length !== 1 || !/^\d+$/.test(current[0].major) || !/^\d+$/.test(current[0].latestMinor))
  throw Error('Ambiguous current PostgreSQL stable release');
const upstreamPostgres = `${current[0].major}.${current[0].latestMinor}`;
const postgres = Bun.spawn([resolve(payload, 'bin/postgres'), '--version'], {
  stdout: 'pipe', stderr: 'pipe',
  env: {...process.env, LD_LIBRARY_PATH: [resolve(payload, 'lib'), process.env.LD_LIBRARY_PATH].filter(Boolean).join(':')},
});
const [postgresStatus, postgresVersion] = await Promise.all([postgres.exited, new Response(postgres.stdout).text(), new Response(postgres.stderr).text()]);
const selectedPostgres = /^postgres \(PostgreSQL\) (\d+\.\d+)(?:\s|$)/.exec(postgresVersion)?.[1];
if (postgresStatus !== 0 || selectedPostgres !== upstreamPostgres)
  throw Error(`Native PostgreSQL payload must match official current stable ${upstreamPostgres}`);
const postgresSelection = {checked_at: new Date().toISOString(), source: versionsResponse.url, version: selectedPostgres};
await buildDesktopUI(resolve(root, '.artifacts/desktop-ui'));
await mkdir(resolve(out, 'factory-runtime'), {recursive: true});
await writeFile(resolve(out, 'factory-runtime/postgresql-selection.json'), JSON.stringify(postgresSelection) + '\n');
async function capture(args: string[]) {
  const child = Bun.spawn(args, {cwd: root, stdout: 'pipe', stderr: 'inherit'});
  const [status, result] = await Promise.all([child.exited, new Response(child.stdout).text()]);
  if (status !== 0) throw Error('Build input identity unavailable: ' + args[0]);
  return result.trim();
}
const buildInputs = {
  checked_at: new Date().toISOString(), source_commit: await capture(['git', 'rev-parse', 'HEAD']),
  source_changes: await capture(['git', 'status', '--porcelain']),
  go: await capture(['go', 'version']), rust: await capture(['rustc', '--version']),
  bun: Bun.version, postgresql: postgresSelection,
  profile: values.check ? 'native-check' : 'release',
};
await writeFile(resolve(out, 'build-inputs.json'), JSON.stringify(buildInputs, null, 2) + '\n');
async function run(args: string[]) {
  const child = Bun.spawn(args, {cwd: root, stdout: 'inherit', stderr: 'inherit'});
  if ((await child.exited) !== 0) throw Error('Native desktop build failed');
}
await run([
  'go',
  'build',
  '-mod=readonly',
  '-trimpath',
  '-o',
  resolve(out, 'factory-runtime/soda-factory-server'),
  './cmd/soda-factory-server',
]);
const desktop = Bun.spawn(
  [
    'cargo',
    'build',
    ...(values.check ? [] : ['--release']),
    '--locked',
    '-p',
    'soda-desktop',
    '-p',
    'soda-factory-worker',
    '-p',
    'soda-factory-broker',
    '--bins',
    '--features',
    values.check ? 'soda-desktop/native-check' : 'soda-desktop/native-ui',
    '--message-format=json-render-diagnostics',
  ],
  {cwd: root, stdout: 'pipe', stderr: 'inherit'}
);
const [status, messages] = await Promise.all([desktop.exited, new Response(desktop.stdout).text()]);
const executables = new Map<string, string>();
const packages = new Set<string>();
for (const line of messages.split('\n')) {
  if (!line) continue;
  const message = JSON.parse(line);
  if (message.reason === 'compiler-message' && message.message.rendered) process.stderr.write(message.message.rendered);
  if (message.reason === 'compiler-artifact' && message.executable)
    executables.set(message.target.name, message.executable);
  if (message.reason === 'compiler-artifact') packages.add(message.package_id);
}
if (status !== 0) throw Error('Native desktop build failed');
await writeFile(resolve(out, 'build-inputs.json'), JSON.stringify({...buildInputs, rust_packages: [...packages].sort()}, null, 2) + '\n');
const execution = resolve(out, 'factory-runtime/execution');
await mkdir(resolve(execution, 'libexec'), {recursive: true});
await mkdir(resolve(execution, 'systemd'), {recursive: true});
const executionFiles: string[] = [];
for (const name of ['soda-desktop', 'soda-factory-muse', 'soda-factory-broker', 'soda-factory-preparer',
  'soda-factory-preparation-cleaner', 'soda-factory-agent', 'soda-factory-provider-relay', 'soda-factory-reviewer', 'soda-factory-worker']) {
  const executable = executables.get(name);
  if (!executable) throw Error('Native desktop build did not produce ' + name);
  if (name !== 'soda-desktop' && name !== 'soda-factory-muse') executionFiles.push('libexec\t' + name);
  await copyFile(executable, resolve(name === 'soda-desktop' || name === 'soda-factory-muse' ? out : resolve(execution, 'libexec'), name));
}
for (const owner of ['soda-factory-broker', 'soda-factory-worker']) {
  const directory = resolve(root, 'cmd', owner, 'systemd');
  for (const name of await readdir(directory)) {
    if (!/\.(service|socket)(\.in)?$/.test(name)) throw Error('Unknown execution service asset: ' + name);
    await copyFile(resolve(directory, name), resolve(execution, 'systemd', name));
    executionFiles.push('systemd\t' + name);
  }
}
await writeFile(resolve(execution, 'files.list'), executionFiles.join('\n') + '\n');
await copyFile(resolve(root, 'scripts/install-native-execution.sh'), resolve(execution, 'install.sh'));
await chmod(resolve(execution, 'install.sh'), 0o755);
await cp(payload, resolve(out, 'factory-runtime/postgresql'), {recursive: true, verbatimSymlinks: true});
if (!values.check) await stageDesktopInstaller(out);
console.log('Staged standalone desktop at ' + out);

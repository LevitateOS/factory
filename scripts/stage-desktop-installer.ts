import {chmod, copyFile, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {parseArgs} from 'node:util';
import {copyNotices} from './factory-browser-assets.js';

/** Add the user installer to the existing app stage, without runtime state. */
export async function stageDesktopInstaller(out: string) {
  const root = resolve(import.meta.dir, '..');
  const files = ['soda-desktop', 'soda-factory-muse', 'factory-runtime/soda-factory-server',
    ...['postgres', 'initdb', 'pg_isready'].map(name => 'factory-runtime/postgresql/bin/' + name),
    ...['broker', 'preparer', 'preparation-cleaner', 'agent', 'provider-relay', 'reviewer', 'worker']
      .map(name => 'factory-runtime/execution/libexec/soda-factory-' + name)];
  let machine: number | undefined;
  for (const file of files) {
    const header = new Uint8Array(await Bun.file(resolve(out, file)).slice(0, 20).arrayBuffer());
    if (header.length !== 20 || header[0] !== 0x7f || header[1] !== 0x45 || header[2] !== 0x4c || header[3] !== 0x46 || header[4] !== 2 || header[5] !== 1)
      throw Error('Expected a 64-bit little-endian Linux executable: ' + file);
    const selected = new DataView(header.buffer, header.byteOffset, header.byteLength).getUint16(18, true);
    machine ??= selected;
    if (selected !== machine) throw Error('Mixed executable architectures in desktop stage: ' + file);
  }
  const arch = machine === 62 ? 'x86_64' : machine === 183 ? 'aarch64' : undefined;
  if (!arch) throw Error('Unsupported Linux desktop architecture');
  await writeFile(resolve(out, 'platform'), 'Linux-' + arch + '\n');
  for (const name of ['LICENSE', 'NOTICE']) await copyFile(resolve(root, name), resolve(out, name));
  await copyNotices(out);
  await copyFile(resolve(root, 'assets/branding/icons/hicolor/512x512/apps/soda-os.png'), resolve(out, 'factory.png'));
  await copyFile(resolve(root, 'scripts/install-desktop.sh'), resolve(out, 'install.sh'));
  await chmod(resolve(out, 'install.sh'), 0o755);
}

if (import.meta.main) {
  const {values} = parseArgs({args: Bun.argv.slice(2), options: {stage: {type: 'string'}}});
  if (!values.stage) throw Error('Explicit --stage app directory is required');
  await stageDesktopInstaller(resolve(values.stage));
  console.log('Added user installer to desktop stage');
}

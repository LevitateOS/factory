import {copyFile, cp, mkdir, readFile, readdir, readlink, realpath, stat, writeFile} from 'node:fs/promises';
import {basename, dirname, isAbsolute, relative, resolve, sep} from 'node:path';

const root = resolve(import.meta.dir, '..');
const legalName = /^(?:licen[cs]e|copying|copyright|notice|unlicense)(?:[._-].*)?$/i;
const safePath = /^[a-zA-Z0-9._/@+-]+$/;
const digest = async (path: string) => new Bun.CryptoHasher('sha256').update(await Bun.file(path).arrayBuffer()).digest('hex');

async function output(argv: string[]) {
  const process = Bun.spawn(argv, {cwd: root, stdout: 'pipe', stderr: 'inherit'});
  const [status, text] = await Promise.all([process.exited, new Response(process.stdout).text()]);
  if (status !== 0) throw Error('Distribution input failed: ' + argv[0]);
  return text.trim();
}

/** Record the actual dependency licenses alongside the existing browser notices. */
export async function stageDistributionNotices(out: string) {
  const inventory: {kind: string; name: string; version: string; license: string | undefined; files: string[]; sources?: string[]; source_directory?: string}[] = [];
  async function legal(kind: string, name: string, version: string, directory: string, license?: string, explicit?: string, repository?: string) {
    const destination = resolve(out, 'licenses', kind, name + (version ? '@' + version : ''));
    if (license === 'MPL-2.0') {
      // Retain the covered source itself, not merely a pointer to an upstream
      // repository that might later disappear.
      const url = 'https://www.mozilla.org/media/MPL/2.0/index.f75d2927d3c1.txt';
      const response = await fetch(url, {signal: AbortSignal.timeout(15_000)});
      if (!response.ok) throw Error('Canonical Mozilla license text unavailable');
      const text = await response.text();
      if (!text.startsWith('Mozilla Public License Version 2.0')) throw Error('Mozilla license response invalid');
      await mkdir(destination, {recursive: true});
      await writeFile(resolve(destination, 'MPL-2.0.txt'), text);
      await cp(directory, resolve(destination, 'source'), {recursive: true, filter: path => basename(path) !== '.cargo-ok'});
      inventory.push({kind, name, version, license, files: [relative(out, resolve(destination, 'MPL-2.0.txt'))], sources: [url], source_directory: relative(out, resolve(destination, 'source'))});
      return;
    }
    const files = (await readdir(directory, {withFileTypes: true})).filter(entry => entry.isFile() && legalName.test(entry.name)).map(entry => entry.name);
    if (explicit) {
      const file = relative(directory, resolve(directory, explicit));
      if (!files.includes(file)) files.push(file);
    }
    if (!files.length) {
      for (const name of await readdir(directory)) {
        if (!/^readme(?:\.md|\.txt)?$/i.test(name)) continue;
        const text = await readFile(resolve(directory, name), 'utf8');
        // Retain the entire packaged notice, including its copyright, when
        // this dependency embeds the complete MIT grant in its README.
        if (/copyright/i.test(text) && /permission is hereby granted/i.test(text) && /the software is provided/i.test(text)) files.push(name);
      }
    }
    if (files.length === 0 && repository) {
      // Some published packages omit the repository's legal files. Resolve
      // only their published source commit, never current main.
      const repo = /^https:\/\/github\.com\/([a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+?)(?:\.git)?\/?$/.exec(repository.replace(/^git\+/, ''))?.[1];
      let commit: unknown;
      if (kind === 'rust') {
        const vcs = JSON.parse(await readFile(resolve(directory, '.cargo_vcs_info.json'), 'utf8'));
        commit = vcs.git?.sha1;
      } else if (kind === 'javascript') {
        const response = await fetch(`https://registry.npmjs.org/${encodeURIComponent(name)}/${encodeURIComponent(version)}`, {signal: AbortSignal.timeout(15_000)});
        if (!response.ok) throw Error('Published package provenance unavailable: ' + name);
        const published = await response.json() as {name?: string; version?: string; gitHead?: string};
        if (published.name !== name || published.version !== version) throw Error('Published package identity changed');
        commit = published.gitHead;
      }
      if (!repo || typeof commit !== 'string' || !/^[a-f0-9]{40}$/.test(commit))
        throw Error('Missing exact license provenance for ' + name);
      const response = await fetch(`https://api.github.com/repos/${repo}/contents/?ref=${commit}`, {signal: AbortSignal.timeout(15_000)});
      if (!response.ok) throw Error('Exact dependency license listing unavailable: ' + name);
      const entries: unknown = await response.json();
      if (!Array.isArray(entries)) throw Error('Invalid dependency license listing');
      const sources: string[] = [];
      for (const entry of entries.filter(entry => entry.type === 'file' && legalName.test(entry.name) && safePath.test(entry.name))) {
        const url = `https://raw.githubusercontent.com/${repo}/${commit}/${entry.name}`;
        const response = await fetch(url, {signal: AbortSignal.timeout(15_000)});
        if (!response.ok) throw Error('Exact dependency license text unavailable: ' + name);
        await mkdir(destination, {recursive: true});
        await writeFile(resolve(destination, entry.name), await response.text());
        files.push(entry.name);
        sources.push(url);
      }
      if (!files.length) throw Error('Missing repository license text for ' + name);
      inventory.push({kind, name, version, license, files: files.map(file => relative(out, resolve(destination, file))), sources});
      return;
    }
    if (files.length === 0) throw Error(`Missing license text for ${kind} ${name} ${version}`);
    const copied: string[] = [];
    for (const file of files.sort()) {
      const source = resolve(directory, file);
      if (relative(directory, source).split(sep).includes('..') || !(await stat(source)).isFile())
        throw Error('Dependency license escaped its package: ' + name);
      const target = resolve(destination, file);
      await mkdir(dirname(target), {recursive: true});
      await copyFile(source, target);
      copied.push(relative(out, target));
    }
    inventory.push({kind, name, version, license, files: copied});
  }
  // Include runtime dependency closure, not Storybook or build-only JS tooling.
  const pending = Object.keys(JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8')).dependencies).map(name => ({name, from: root}));
  const visited = new Set<string>();
  while (pending.length) {
    const {name, from} = pending.shift()!;
    let parent = from;
    while (!(await Bun.file(resolve(parent, 'node_modules', name, 'package.json')).exists())) {
      const next = dirname(parent);
      if (next === parent) throw Error('Runtime dependency source unavailable: ' + name);
      parent = next;
    }
    const directory = await realpath(resolve(parent, 'node_modules', name));
    if (visited.has(directory)) continue;
    visited.add(directory);
    const pkg = JSON.parse(await readFile(resolve(directory, 'package.json'), 'utf8'));
    await legal('javascript', pkg.name, pkg.version, directory, pkg.license, undefined, typeof pkg.repository === 'string' ? pkg.repository : pkg.repository?.url);
    pending.push(...Object.keys(pkg.dependencies ?? {}).map(name => ({name, from: directory})));
  }
  const cargo = JSON.parse(await output(['cargo', 'metadata', '--locked', '--format-version=1', '--features', 'soda-desktop/native-ui']));
  const inputs = JSON.parse(await readFile(resolve(out, 'build-inputs.json'), 'utf8'));
  if (!Array.isArray(inputs.rust_packages) || !inputs.rust_packages.length) throw Error('Compiled Rust package inventory is required');
  const compiled = new Set(inputs.rust_packages);
  for (const pkg of cargo.packages) {
    if (!pkg.source || !compiled.has(pkg.id)) continue;
    await legal('rust', pkg.name, pkg.version, dirname(pkg.manifest_path), pkg.license ?? undefined, pkg.license_file ?? undefined, pkg.repository ?? undefined);
  }
  const goModules = new Map<string, string>();
  for (const line of (await output(['go', 'version', '-m', resolve(out, 'factory-runtime/soda-factory-server')])).split('\n')) {
    const fields = line.split('\t');
    if (fields[1] === 'dep' && fields[2] && fields[3]) goModules.set(fields[2], fields[3]);
  }
  if (!goModules.size) throw Error('Compiled Go dependency inventory is required');
  for (const line of (await output(['go', 'list', '-mod=readonly', '-m', '-f', '{{.Path}}\t{{.Version}}\t{{.Dir}}', ...goModules.keys()])).split('\n')) {
    const [name, version, directory] = line.split('\t');
    if (!name || !directory) throw Error('Go module source required for notices: ' + name);
    if (goModules.get(name) !== version) throw Error('Go module differs from built binary: ' + name);
    await legal('go', name, version ?? '', directory);
  }
  const goroot = await output(['go', 'env', 'GOROOT']);
  await legal('toolchains', 'go', (await output(['go', 'version'])).split(' ')[2] ?? '', goroot);
  const rustroot = await output(['rustc', '--print', 'sysroot']);
  const rustNotices = resolve(rustroot, 'share/doc/rust');
  const rustDestination = resolve(out, 'licenses/toolchains/rust');
  await mkdir(rustDestination, {recursive: true});
  for (const name of ['COPYRIGHT.html', 'COPYRIGHT-library.html'])
    await copyFile(resolve(rustNotices, name), resolve(rustDestination, name));
  await legal('postgresql', 'postgresql', '', resolve(out, 'factory-runtime/postgresql'));
  await writeFile(resolve(out, 'licenses/dependencies.json'), JSON.stringify(inventory, null, 2) + '\n');
}

/** One complete file/link inventory is also the installer's same-bundle identity. */
export async function writeDistributionIntegrity(out: string) {
  const files: string[] = [];
  const links: string[] = [];
  async function visit(directory: string) {
    for (const entry of await readdir(directory, {withFileTypes: true})) {
      const path = resolve(directory, entry.name), name = relative(out, path);
      if (!safePath.test(name) || name.split('/').includes('..')) throw Error('Unsafe distribution path: ' + name);
      if (name === 'bundle.sha256' || name === 'bundle-links.list') continue;
      if (entry.isDirectory()) await visit(path);
      else if (entry.isFile()) files.push(name);
      else if (entry.isSymbolicLink()) {
        const target = await readlink(path), resolved = relative(out, await realpath(path));
        if (isAbsolute(target) || !safePath.test(target) || resolved === '..' || resolved.startsWith('..' + sep) || isAbsolute(resolved))
          throw Error('Distribution link escaped bundle: ' + name);
        links.push(name + '\t' + target);
      } else throw Error('Unsupported distribution file: ' + name);
    }
  }
  await visit(out);
  await writeFile(resolve(out, 'bundle-links.list'), links.sort().join('\n') + (links.length ? '\n' : ''));
  files.push('bundle-links.list');
  const sums = [];
  for (const name of files.sort()) sums.push(await digest(resolve(out, name)) + '  ' + name);
  await writeFile(resolve(out, 'bundle.sha256'), sums.join('\n') + '\n');
}

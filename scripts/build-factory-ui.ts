import assert from 'node:assert/strict';
import {mkdir, readFile, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {parseArgs} from 'node:util';
import {copyRolePortraits, copyNotices, filesUnder, restoreNativeSchemeCalls} from './factory-browser-assets.js';

const root = resolve(import.meta.dir, '..');
const litEntry = resolve(root, 'frontend/factory/app/lit.ts');
/** Production settings for every module of the shared UI and its hosts: Lit in
 * production mode, loaded once from the shared `lit.js`. */
export const sharedModule = {
  define: {'process.env.NODE_ENV': '"production"'},
  lit: {
    name: 'shared-lit',
    setup(build) {
      build.onResolve({filter: /^lit(\/.*)?$/}, (args) => (args.importer === litEntry ? undefined : {path: './lit.js', external: true}));
    },
  } satisfies Bun.BunPlugin,
};
/** Production component module and its offline envelope. The host supplies the
 * bootstrap, authenticated ports, navigation and optional terminal renderer. */
export async function buildFactoryUI(out: string): Promise<{files: string[]; sources: string[]}> {
  await mkdir(out, {recursive: true});
  const sources = new Set<string>();
  const boundary: Bun.BunPlugin = {
    name: 'factory-product-boundary',
    setup(build) {
      build.onLoad({filter: /.*/}, (args) => {
        const path = resolve(args.path);
        if (path.startsWith(resolve(root, 'frontend') + '/')) {
          assert(
            path.startsWith(resolve(root, 'frontend/factory') + '/') && !path.endsWith('.stories.ts'),
            `Factory product imports a host or story: ${path}`
          );
          sources.add(path.slice(root.length + 1));
        }
        if (path.startsWith(resolve(root, 'assets') + '/'))
          assert(
            path.startsWith(resolve(root, 'assets/branding/fonts') + '/') ||
              path.startsWith(resolve(root, 'assets/branding/theme') + '/'),
            `Factory CSS imports host assets: ${path}`
          );
        return undefined;
      });
    },
  };
  const built = await Promise.all([
    Bun.build({
      entrypoints: [resolve(root, 'frontend/factory/app/browser.ts')],
      outdir: out,
      target: 'browser',
      format: 'esm',
      minify: true,
      naming: {entry: 'factory-app.js'},
      define: sharedModule.define,
      plugins: [sharedModule.lit, boundary],
    }),
    Bun.build({
      entrypoints: [litEntry],
      outdir: out,
      target: 'browser',
      format: 'esm',
      minify: true,
      naming: {entry: 'lit.js'},
      define: sharedModule.define,
      plugins: [boundary],
    }),
    Bun.build({
      entrypoints: [resolve(root, 'frontend/factory/styles/base.css')],
      outdir: out,
      target: 'browser',
      minify: true,
      naming: {entry: 'factory.css'},
      plugins: [boundary],
    }),
  ]);
  for (const result of built) assert(result.success, result.logs.join('\n'));
  // Native color-scheme belongs to the envelope, including WebKit. Keep the
  // canonical root tokens; only Forge's extension strips document-level rules.
  const css = resolve(out, 'factory.css');
  await writeFile(css, restoreNativeSchemeCalls(await readFile(css, 'utf8')));
  await copyRolePortraits(out);
  await copyNotices(out);
  return {files: await filesUnder(out), sources: [...sources].sort()};
}
if (import.meta.main) {
  const {values} = parseArgs({args: Bun.argv.slice(2), options: {out: {type: 'string'}}});
  const out = resolve(root, values.out ?? '.artifacts/factory-ui');
  const result = await buildFactoryUI(out);
  console.log(`Built ${result.files.length} Factory UI assets from ${result.sources.length} shared sources in ${out}`);
}

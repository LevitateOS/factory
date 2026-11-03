// Development-only bundle: upstream analyzer omits its classic TypeScript runtime
// dependency. Resolve *every* bare compiler import to this workspace's pinned copy,
// not the product TS7 distribution. No patched packages or Node runtime required.
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';

const root = resolve(import.meta.dir, '..');
const tool = createRequire(resolve(root, 'tools/lit-check/package.json'));
const compiler = tool.resolve('typescript');
const out = resolve(root, '.artifacts/lit-check/check.js');
let compilerImports = 0;
const result = await Bun.build({
  entrypoints: [resolve(root, 'tools/lit-check/check.ts')],
  target: 'bun',
  format: 'esm',
  plugins: [
    {
      name: 'analysis-only-compiler',
      setup(build) {
        build.onResolve({filter: /^typescript(?:\/|$)/}, (args) => {
          compilerImports++;
          return {path: tool.resolve(args.path), external: true};
        });
        // Keep UMD language-service packages in their installed directories: their
        // relative runtime require() calls are not statically bundleable.
        build.onResolve({filter: /^[^./]/}, (args) => {
          if (
            args.kind === 'entry-point-build' ||
            /^(?:node:|lit-analyzer(?:\/|$)|web-component-analyzer(?:\/|$)|ts-simple-type(?:\/|$))/.test(args.path)
          )
            return;
          return {path: createRequire(args.importer).resolve(args.path), external: true};
        });
      },
    },
  ],
});
assert(result.success, result.logs.join('\n'));
assert(compilerImports > 1, 'Analyzer compiler imports were not bound');
const [output] = result.outputs;
assert(output && result.outputs.length === 1);
await mkdir(resolve(root, '.artifacts/lit-check'), {recursive: true});
await Bun.write(out, output);
const child = Bun.spawn([process.execPath, out, ...Bun.argv.slice(2)], {
  cwd: root,
  env: {...process.env, SODA_ANALYSIS_COMPILER: compiler},
  stdout: 'inherit',
  stderr: 'inherit',
});
process.exit(await child.exited);

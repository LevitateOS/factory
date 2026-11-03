import {buildFactoryUI} from './build-factory-ui.js';
import {resolve} from 'node:path';
import {copyFile} from 'node:fs/promises';
export async function buildDesktopUI(out: string) {
  const product = await buildFactoryUI(out);
  const built = await Bun.build({
    entrypoints: [resolve(import.meta.dir, '../frontend/desktop/main.ts')],
    outdir: out,
    target: 'browser',
    format: 'esm',
    minify: true,
    naming: {entry: 'desktop.js'},
    plugins: [
      {
        name: 'shared-production-entry',
        setup(build) {
          build.onResolve({filter: /factory\/app\/browser\.js$/}, () => ({path: './factory-app.js', external: true}));
        },
      },
    ],
  });
  if (!built.success) throw Error(built.logs.join('\n'));
  for (const file of ['index.html', 'desktop.css'])
    await copyFile(resolve(import.meta.dir, '../frontend/desktop', file), resolve(out, file));
  return product;
}
if (import.meta.main) {
  await buildDesktopUI(resolve(import.meta.dir, '../.artifacts/desktop-ui'));
  console.log('Built desktop bootstrap with the production shared UI');
}

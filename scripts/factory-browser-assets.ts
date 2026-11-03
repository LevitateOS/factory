import assert from 'node:assert/strict';
import {copyFile, mkdir, readdir, readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {feedRolePortraits} from '../frontend/factory/feed/feed-portraits.js';
const root = resolve(import.meta.dir, '..');
const source = (path: string) => resolve(root, path);

function closingParen(css: string, open: number): number {
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '(') depth++;
    else if (css[i] === ')') {
      depth--;
      if (depth === 0) return i;
    }
  }
  throw new Error('Unbalanced parentheses in emitted stylesheet');
}

// Bun lowers light-dark() to guard-variable fragments but emits
// the selection guards only alongside in-graph color-scheme rules. Native
// scheme selection lives outside this compilation graph, so restore the native
// call and let the browser resolve it against the external selection.
export function restoreNativeSchemeCalls(css: string): string {
  // These generated declarations are unused after restoring the native calls.
  // Standalone CSS includes its color-scheme envelope in the same graph.
  css = css.replace(/--buncss-(?:light|dark):(?:initial)?\s*(?:;|(?=\}))/g, '');
  const lightMarker = 'var(--buncss-light,';
  const darkMarker = 'var(--buncss-dark,';
  let output = '';
  let rest = css;
  for (;;) {
    const light = rest.indexOf(lightMarker);
    if (light < 0) {
      assert(!rest.includes('--buncss-'), 'Unpaired lowered scheme fragment in emitted stylesheet');
      return output + rest;
    }
    const lightEnd = closingParen(rest, light + 3);
    let dark = lightEnd + 1;
    while (rest[dark] === ' ' || rest[dark] === '\n' || rest[dark] === '\t' || rest[dark] === '\r') dark++;
    if (!rest.startsWith(darkMarker, dark)) throw new Error('Unpaired lowered scheme fragment in emitted stylesheet');
    const darkEnd = closingParen(rest, dark + 3);
    output += `${rest.slice(0, light)}light-dark(${rest.slice(light + lightMarker.length, lightEnd)},${rest.slice(dark + darkMarker.length, darkEnd)})`;
    rest = rest.slice(darkEnd + 1);
  }
}

export async function copyRolePortraits(assets: string) {
  for (const file of Object.values(feedRolePortraits)) {
    const destination = resolve(assets, file);
    await mkdir(dirname(destination), {recursive: true});
    await copyFile(source(`assets/branding/${file}`), destination);
  }
}

export async function copyNotices(assets: string) {
  const notices = resolve(assets, 'licenses');
  await mkdir(resolve(notices, 'fonts'), {recursive: true});
  const fonts = await readFile(source('assets/branding/fonts/fonts.css'), 'utf8');
  const families = new Set(
    [...fonts.matchAll(/url\("\.\/([a-z0-9-]+)\/[a-z0-9.-]+\.woff2"\)/g)].map((match) => match[1])
  );
  assert(families.size > 0, 'Expected local font families');
  for (const family of families) {
    await copyFile(source(`assets/branding/fonts/${family}/LICENSE`), resolve(notices, 'fonts', `${family}.LICENSE`));
  }
  await copyFile(source('assets/branding/fonts/sources.json'), resolve(notices, 'font-sources.json'));
  await copyFile(source('system/licenses/lit-LICENSE'), resolve(notices, 'lit.LICENSE'));
  await copyFile(source('node_modules/marked/LICENSE'), resolve(notices, 'marked.LICENSE'));
  await copyFile(source('node_modules/dompurify/LICENSE'), resolve(notices, 'dompurify.LICENSE'));
  await copyFile(source('node_modules/dompurify/LICENSE-MPL'), resolve(notices, 'dompurify-MPL.LICENSE'));
}

export async function filesUnder(path: string, prefix = ''): Promise<string[]> {
  const files: string[] = [];
  for (const entry of await readdir(path, {withFileTypes: true})) {
    const name = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) files.push(...(await filesUnder(resolve(path, entry.name), name)));
    else {
      assert(entry.isFile(), `Unsafe generated asset ${name}`);
      files.push(name);
    }
  }
  return files;
}

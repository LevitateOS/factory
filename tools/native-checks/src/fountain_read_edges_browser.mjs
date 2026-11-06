// The existing real Fountain edge fixture, viewed through production shared PR pages.
import {readFile, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';

const root = process.argv[2];
const input = JSON.parse(await readFile(resolve(root, 'edge-browser-input.json'), 'utf8'));
const {chromium} = await import(pathToFileURL(input.module).href);
const browser = await chromium.launch({headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
const context = await browser.newContext({ignoreHTTPSErrors: true, viewport: {width: 1280, height: 900}});
const page = await context.newPage();
page.setDefaultTimeout(20000);
let stage = 'login';
const save = (name, value) => writeFile(resolve(root, name), JSON.stringify(value), {mode: 0o600, flag: 'wx'});
const assert = (ok, message) => { if (!ok) throw Error(message); };
try {
  await page.goto(input.origin + '/user/login');
  await page.locator('input[name="user_name"]').fill('factory-check');
  await page.locator('input[name="password"]').fill(await readFile(resolve(root, 'edge-browser-password'), 'utf8'));
  await page.locator('form').filter({has: page.locator('input[name="user_name"]')})
    .getByRole('button', {name: /^sign in$/i}).click();
  await page.waitForURL(url => !url.pathname.startsWith('/user/login'));
  stage = 'first PR files';
  await page.goto(input.origin + '/factory-check/edges-one/feed/work/pulls/1');
  const detail = page.locator('soda-factory-pull');
  await detail.getByRole('heading', {name: 'First native PR number one', exact: true}).waitFor();
  await detail.getByRole('button', {name: 'Files', exact: true}).click();
  await detail.locator('soda-factory-diff').first().waitFor();
  assert(await detail.locator('soda-factory-diff').count() === 6, 'six-file catalogue not rendered');
  const file = name => detail.locator('soda-factory-diff')
    .filter({has: page.getByRole('heading', {name, exact: true})});
  await file('large.txt').getByText('The provider truncated this patch. Line comments are unavailable.', {exact: true}).waitFor();
  await file('blob.bin').getByText('Binary file. No text patch is available.', {exact: true}).waitFor();
  await file('renamed-名前.txt').getByText('Previously old.txt', {exact: true}).waitFor();
  await file('delete.txt').getByRole('heading', {name: 'delete.txt', exact: true}).waitFor();
  await file(':(glob)literal-名前[1].txt').getByRole('heading', {name: ':(glob)literal-名前[1].txt', exact: true}).waitFor();
  const modified = file('modify.txt');
  await modified.getByRole('button', {name: 'Toggle diff layout'}).click();
  await modified.getByRole('button', {name: 'Toggle diff layout'}).filter({hasText: 'Split view'}).waitFor();
  stage = 'same-number repository';
  await page.goto(input.origin + '/factory-check/edges-two/feed/work/pulls/1');
  const other = page.locator('soda-factory-pull');
  await other.getByRole('heading', {name: 'Second native PR number one', exact: true}).waitFor();
  await other.getByRole('button', {name: 'Files', exact: true}).click();
  await other.locator('soda-factory-diff').first().waitFor();
  assert(await other.locator('soda-factory-diff').count() === 1, 'same-number PR files crossed repositories');
  await save('edge-browser-result.json', {result: 'pass', large_partial: true, binary: true,
    rename_delete_literal: true, split_unified: true, same_number_isolation: true});
  console.log('PASS: real Fountain shared PR edge files and same-number repository isolation');
} catch (error) {
  await save('edge-browser-error.json', {stage, error_class: error?.name ?? 'Error',
    message: String(error?.message ?? error).slice(0, 512)});
  console.error('FAIL: native Fountain PR edge browser stage=' + stage);
  process.exitCode = 1;
} finally {
  await context.close();
  await browser.close();
}

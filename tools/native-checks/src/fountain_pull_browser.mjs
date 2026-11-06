// Actual local Fountain host, installed package and shared production PR UI.
// The private reviewer logs in through the native page; provider writes use Factory.
import {readFile, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';

const root = process.argv[2];
const input = JSON.parse(await readFile(resolve(root, 'pull-browser-input.json'), 'utf8'));
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
  await page.locator('input[name="user_name"]').fill('pull-reader');
  await page.locator('input[name="password"]').fill(await readFile(resolve(root, 'pull-browser-password'), 'utf8'));
  await page.locator('form').filter({has: page.locator('input[name="user_name"]')})
    .getByRole('button', {name: /^sign in$/i}).click();
  await page.waitForURL(url => !url.pathname.startsWith('/user/login'));
  const repository = '/factory-check/pull-code';
  const list = repository + '/feed/work/pulls';
  const detailPath = list + '/2';
  stage = 'navigation';
  await page.goto(input.origin + repository + '/feed');
  const pullsTab = page.locator('a[data-soda-place="pulls"]');
  await pullsTab.waitFor();
  assert(await pullsTab.getAttribute('href') === list, 'native PR header did not use the shared route');
  await pullsTab.click();
  await page.waitForURL(url => url.pathname === list);
  await page.locator('soda-factory-pulls').getByRole('button', {name: 'Native code review', exact: true}).click();
  await page.waitForURL(url => url.pathname === detailPath);
  const detail = page.locator('soda-factory-pull');
  await detail.getByRole('heading', {name: 'Native code review', exact: true}).waitFor();
  stage = 'commits';
  await detail.getByRole('button', {name: 'Commits', exact: true}).click();
  await detail.locator('section[aria-label="Commits"] li').first().waitFor();
  assert(await detail.locator('section[aria-label="Commits"] li').count() === 3, 'shared commit history incomplete');
  stage = 'files';
  await detail.getByRole('button', {name: 'Files', exact: true}).click();
  await detail.locator('soda-factory-diff').first().waitFor();
  assert(await detail.locator('soda-factory-diff').count() === 3, 'shared file catalogue incomplete');
  await detail.getByText('Binary file. No text patch is available.', {exact: true}).waitFor();
  stage = 'reviews';
  await detail.getByRole('button', {name: 'Reviews', exact: true}).click();
  await detail.locator('section[aria-label="Reviews"] li').first().waitFor();
  assert(await detail.locator('section[aria-label="Reviews"] li').count() === 1, 'private pending review leaked to reviewer');
  stage = 'threads';
  await detail.getByRole('button', {name: 'Inline discussions', exact: true}).click();
  await detail.getByText('actual native inline reply', {exact: true}).waitFor();
  assert(await detail.locator('section[aria-label="Inline discussions"] > ol > li').count() === 2,
    'shared inline discussions or pending privacy incomplete');
  stage = 'checks';
  await detail.getByRole('button', {name: 'Checks', exact: true}).click();
  await detail.locator('section[aria-label="Checks"]').waitFor();
  stage = 'approval';
  const review = detail.locator('soda-factory-pull-review');
  await review.getByRole('button', {name: 'Approve', exact: true}).click();
  await review.getByText('Review submitted.', {exact: true}).waitFor();
  await detail.getByRole('button', {name: 'Reviews', exact: true}).click();
  await detail.locator('section[aria-label="Reviews"] li').nth(1).waitFor();
  await page.reload();
  await detail.locator('section[aria-label="Reviews"] li').nth(1).waitFor();
  await save('pull-browser-result.json', {
    result: 'pass', native_header_and_shared_list: true, detail: true,
    commits: 3, files: 3, checks: true, inline_discussions: 2,
    pending_review_private: true, approval_submitted: true, approval_readback_after_reload: true,
  });
  console.log('PASS: real Fountain shared PR list/detail/commits/files/checks/threads and non-author approval');
} catch (error) {
  await save('pull-browser-error.json', {stage, error_class: error?.name ?? 'Error', message: String(error?.message ?? error).slice(0, 512)});
  console.error('FAIL: native Fountain PR browser stage=' + stage);
  process.exitCode = 1;
} finally {
  await context.close();
  await browser.close();
}

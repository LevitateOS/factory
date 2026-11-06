// Actual local Fountain host, installed package and shared production PR UI.
// The private reviewer logs in through the native page; provider writes use Factory.
import {readFile, writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';

const root = process.argv[2];
const merge = process.argv[3] === 'merge';
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
  if (merge) {
    stage = 'merge';
    await page.goto(input.origin + detailPath);
    const detail = page.locator('soda-factory-pull');
    await detail.getByRole('heading', {name: 'Native code review', exact: true}).waitFor();
    const control = detail.locator('soda-factory-pull-merge');
    await control.getByRole('combobox', {name: 'Merge method'}).selectOption('merge');
    await control.getByRole('textbox', {name: 'Merge message'}).fill('Merge reviewed native code');
    await control.getByRole('button', {name: 'Merge pull request'}).click();
    await control.getByText('Merge recorded. Refresh to read the provider state.', {exact: true}).waitFor();
    await page.reload();
    await detail.locator('.soda-pull-meta').filter({hasText: '· merged'}).waitFor();
    await save('pull-browser-merge-result.json', {result: 'pass', merged: true, readback_after_reload: true});
    console.log('PASS: real Fountain shared PR merge and provider readback');
  } else {
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
  await detail.locator('article[aria-label="Pull request description"]')
    .getByRole('link', {name: 'Related issue', exact: true}).click();
  await page.waitForURL(url => url.pathname === repository + '/feed/work/issues/1');
  await page.locator('soda-factory-issue')
    .getByRole('heading', {name: 'Ordinary issue, not a PR', exact: true}).waitFor();
  await page.goBack();
  await page.waitForURL(url => url.pathname === detailPath);
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
  stage = 'thread resolution';
  const discussion = detail.locator('section[aria-label="Inline discussions"] > ol > li')
    .filter({hasText: 'actual native inline reply'});
  await discussion.getByRole('button', {name: 'Manage resolution'}).click();
  await discussion.getByRole('button', {name: 'Resolve discussion'}).click();
  await discussion.filter({hasText: '· resolved'}).waitFor();
  await discussion.getByRole('button', {name: 'Manage resolution'}).click();
  await discussion.getByRole('button', {name: 'Reopen discussion'}).click();
  await discussion.filter({hasText: '· unresolved'}).waitFor();
  stage = 'checks';
  await detail.getByRole('button', {name: 'Checks', exact: true}).click();
  await detail.locator('section[aria-label="Checks"]').waitFor();
  stage = 'draft review';
  const review = detail.locator('soda-factory-pull-review');
  await review.getByRole('textbox', {name: 'Review Markdown'}).fill('Initial draft review');
  await review.getByRole('button', {name: 'Save draft review'}).click();
  await review.getByText('Draft review saved. Edit it here or choose a submission action.', {exact: true}).waitFor();
  await detail.getByText('Loading pull request…', {exact: true}).waitFor({state: 'hidden'});
  await detail.getByRole('button', {name: 'Reviews', exact: true}).click();
  await detail.locator('section[aria-label="Reviews"]').waitFor();
  const reviewRows = detail.locator('section[aria-label="Reviews"] li');
  const editDraft = detail.getByRole('button', {name: 'Edit saved draft review'});
  await editDraft.waitFor({timeout: 5000}).catch(async () => {
    throw Error('saved draft editor missing: ' + (await reviewRows.allTextContents()).map(row => row.slice(0, 100)).join(' | '));
  });
  await editDraft.click();
  await review.getByRole('textbox', {name: 'Review Markdown'}).fill('Reviewed and approved after draft');
  stage = 'submit pending review';
  await review.getByRole('button', {name: 'Approve', exact: true}).click();
  await review.getByText('Review submitted.', {exact: true}).waitFor();
  await detail.locator('section[aria-label="Reviews"] li').nth(1).waitFor();
  assert(await detail.locator('section[aria-label="Reviews"] li').count() === 2, 'draft submission created a second review');
  await detail.locator('section[aria-label="Reviews"] li').filter({hasText: 'Reviewed and approved after draft'}).waitFor();
  await page.reload();
  await detail.locator('section[aria-label="Reviews"] li').nth(1).waitFor();
  await detail.locator('section[aria-label="Reviews"] li').filter({hasText: 'Reviewed and approved after draft'}).waitFor();
  await save('pull-browser-result.json', {
    result: 'pass', native_header_and_shared_list: true, detail: true,
    markdown_issue_navigation: true,
    commits: 3, files: 3, checks: true, inline_discussions: 2, thread_resolved_and_reopened: true,
    pending_review_private: true, draft_reopened_and_submitted: true,
    approval_submitted: true, approval_readback_after_reload: true,
  });
  console.log('PASS: real Fountain shared PR reads and draft review reopening/submission');
  }
} catch (error) {
  await save(merge ? 'pull-browser-merge-error.json' : 'pull-browser-error.json',
    {stage, error_class: error?.name ?? 'Error', message: String(error?.message ?? error).slice(0, 512)});
  console.error('FAIL: native Fountain PR browser stage=' + stage);
  process.exitCode = 1;
} finally {
  await context.close();
  await browser.close();
}

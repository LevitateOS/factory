// Actual local Fountain host, installed package and shared production components.
// No intercepted API, synthetic admission headers or retained account is used.
import {readFile, writeFile, access} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';

const root = process.argv[2];
const input = JSON.parse(await readFile(resolve(root, 'browser-input.json'), 'utf8'));
const {chromium} = await import(pathToFileURL(input.module).href);
const profile = resolve(root, 'issue-browser-profile');
const options = {headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
  ignoreHTTPSErrors: true, viewport: {width: 1280, height: 900}};
let context = await chromium.launchPersistentContext(profile, options);
let page = context.pages()[0] || await context.newPage();
page.setDefaultTimeout(20000);
let stage = 'login';
const save = (name, value) => writeFile(resolve(root, name), JSON.stringify(value), {mode: 0o600, flag: 'wx'});
const assert = (ok, message) => {
  if (!ok) {
    const error = Error(message);
    error.check = message;
    throw error;
  }
};
const until = async (check, label) => {
  const end = Date.now() + 20000;
  for (;;) {
    if (await check()) return;
    if (Date.now() >= end) throw Error(label);
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
};
const login = async () => {
  await page.goto(input.origin + '/user/login');
  if (!new URL(page.url()).pathname.startsWith('/user/login')) return;
  await page.locator('input[name="user_name"]').fill(input.username);
  await page.locator('input[name="password"]').fill(await readFile(resolve(root, 'browser-password'), 'utf8'));
  await page
    .locator('form')
    .filter({has: page.locator('input[name="user_name"]')})
    .getByRole('button', {name: /^sign in$/i})
    .click();
  await page.waitForURL((url) => !url.pathname.startsWith('/user/login'));
};
try {
  await login();
  stage = 'header';
  const repository = '/factory-check/read-one-renamed';
  const list = repository + '/feed/work/issues';
  const issue = list + '/' + input.number;
  await page.goto(input.origin + repository + '/feed');
  const issuesTab = page.locator('a[data-soda-place="issues"]');
  await issuesTab.waitFor();
  assert((await issuesTab.getAttribute('href')) === list, 'native Issues header did not use the shared route');
  await issuesTab.click();
  await page.waitForURL((url) => url.pathname === list);
  stage = 'list';
  await page.locator('soda-factory-issues').getByRole('button', {name: 'Rendered native read', exact: true}).click();
  await page.waitForURL((url) => url.pathname === issue);
  let detail = page.locator('soda-factory-issue');
  const description = detail.locator('article[aria-label="Issue description"]');
  const comment = detail.locator('#issuecomment-' + input.comment);
  await comment.getByRole('heading', {name: 'Native comment', exact: true}).waitFor();
  stage = 'markdown';
  assert(
    (await description.getByRole('heading', {name: 'Native heading', exact: true}).count()) === 1,
    'native issue Markdown absent'
  );
  assert(
    (await detail.locator('script,iframe,img,[onclick],[onerror],a[href^="javascript:"]').count()) === 0,
    'unsafe Markdown DOM retained'
  );
  assert(
    (await description.getByRole('link', {name: 'File', exact: true}).getAttribute('href')).endsWith(
      repository + '/README.md'
    ),
    'native repository-relative link differs'
  );
  assert(
    (await description.getByRole('link', {name: 'Branch', exact: true}).getAttribute('href')).endsWith(
      repository + '/src/branch/main/README.md'
    ),
    'explicit native branch link differs'
  );
  assert(
    (await description.getByRole('link', {name: 'Attachment', exact: true}).count()) === 1,
    'attachment not explicit'
  );
  await page.locator('soda-factory-work-journey button').waitFor();
  stage = 'facts';
  await page.locator('soda-factory-work-journey').getByText('No journey sections recorded.', {exact: true}).waitFor();
  const heading = await description.locator('h1').getAttribute('id');
  const scroll = (id) =>
    until(
      () =>
        detail.locator('[id="' + id + '"]').evaluate((node) => {
          const box = node.getBoundingClientRect();
          return box.top >= -2 && box.top < innerHeight;
        }),
      'fragment did not scroll'
    );
  stage = 'fragments';
  await description.getByRole('link', {name: 'Heading', exact: true}).click();
  await page.waitForURL((url) => url.pathname === issue && decodeURIComponent(url.hash) === '#' + heading);
  await scroll(heading);
  await description.getByRole('link', {name: 'Comment', exact: true}).click();
  await page.waitForURL((url) => url.hash === '#issuecomment-' + input.comment);
  await scroll('issuecomment-' + input.comment);
  await page.goBack();
  await page.waitForURL((url) => decodeURIComponent(url.hash) === '#' + heading);
  await scroll(heading);
  await page.goForward();
  await page.waitForURL((url) => url.hash === '#issuecomment-' + input.comment);
  stage = 'factory';
  await page.locator('nav[aria-label="Issue navigation"]').getByRole('link', {name: 'Factory', exact: true}).click();
  await page.locator('.soda-issue-factory-controls[open]').waitFor();
  await page.goBack();
  await comment.waitFor();
  await page.reload();
  await comment.waitFor();
  await scroll('issuecomment-' + input.comment);
  await page.locator('soda-factory-work-journey').getByText('No journey sections recorded.', {exact: true}).waitFor();
  assert(
    (
      await page
        .locator('nav[aria-label="Issue navigation"]')
        .getByRole('link', {name: 'Open in Fountain', exact: true})
        .getAttribute('href')
    ).endsWith(repository + '/issues/' + input.number),
    'native original unavailable'
  );
  let editor = page.locator('soda-factory-issue-editor');
  let composer = page.locator('soda-factory-issue-comment');
  assert(await editor.getByRole('button', {name: 'Edit issue', exact: true}).isDisabled(), 'reader could edit issue');
  assert(await composer.getByRole('button', {name: 'Post comment', exact: true}).isDisabled(), 'reader could post comment');
  await page.screenshot({path: resolve(root, 'issue-browser.png')});
  stage = 'write-wait';
  await save('issue-browser-read-ready', {read: true});
  await until(async () => {
    try { await access(resolve(root, 'issue-browser-write-ready')); return true; }
    catch { return false; }
  }, 'write grant barrier');
  stage = 'write';
  await page.reload();
  await detail.getByText('Rendered native read', {exact: true}).waitFor();
  await editor.getByRole('button', {name: 'Edit issue', exact: true}).click();
  await editor.getByRole('textbox', {name: 'Edit issue title'}).fill('Unsent native title');
  await editor.getByRole('textbox', {name: 'Edit issue Markdown'}).fill('Unsent native Markdown draft 茶');
  await page.waitForFunction(() => Object.keys(localStorage).some(key =>
    key.includes('issue-edit') && localStorage.getItem(key)?.includes('Unsent native Markdown draft 茶')));
  stage = 'draft-restart';
  await context.close();
  context = await chromium.launchPersistentContext(profile, options);
  page = context.pages()[0] || await context.newPage();
  page.setDefaultTimeout(20000);
  await login();
  await page.goto(input.origin + issue);
  detail = page.locator('soda-factory-issue');
  editor = page.locator('soda-factory-issue-editor');
  composer = page.locator('soda-factory-issue-comment');
  await editor.getByRole('textbox', {name: 'Edit issue Markdown'}).waitFor();
  assert(await editor.getByRole('textbox', {name: 'Edit issue title'}).inputValue() === 'Unsent native title' &&
    await editor.getByRole('textbox', {name: 'Edit issue Markdown'}).inputValue() === 'Unsent native Markdown draft 茶',
  'unsent issue draft lost after browser restart');
  stage = 'write';
  await composer.getByRole('button', {name: 'Write comment', exact: true}).click();
  await composer.getByRole('textbox', {name: 'Comment Markdown'}).fill('Written through the shared issue composer.');
  await composer.getByRole('button', {name: 'Post comment', exact: true}).click();
  await detail.getByText('Written through the shared issue composer.', {exact: true}).waitFor();
  await editor.getByRole('textbox', {name: 'Edit issue title'}).fill('Rendered native edit');
  await editor.getByRole('textbox', {name: 'Edit issue Markdown'}).fill('# Edited native description\n\nSaved through the shared editor.');
  await editor.getByRole('button', {name: 'Save issue', exact: true}).click();
  await detail.getByRole('heading', {name: 'Rendered native edit', exact: true}).waitFor();
  await detail.locator('article[aria-label="Issue description"]').getByText('Saved through the shared editor.', {exact: true}).waitFor();
  await page.reload();
  await detail.getByRole('heading', {name: 'Rendered native edit', exact: true}).waitFor();
  await detail.locator('article[aria-label="Issue description"]').getByText('Saved through the shared editor.', {exact: true}).waitFor();
  await detail.getByText('Written through the shared issue composer.', {exact: true}).waitFor();
  stage = 'issue-state';
  const state = page.locator('soda-factory-issue-state');
  await state.getByRole('button', {name: 'Close issue', exact: true}).click();
  await until(async () => /·\s*Closed\s*·/.test(await detail.locator('.soda-issue-meta').first().textContent()), 'issue did not close');
  await state.getByRole('button', {name: 'Reopen issue', exact: true}).click();
  await until(async () => /·\s*Open\s*·/.test(await detail.locator('.soda-issue-meta').first().textContent()), 'issue did not reopen');
  stage = 'revocation-wait';
  await save('issue-browser-ready', {read: true, comment: true, edit: true});
  await until(async () => {
    try {
      await access(resolve(root, 'issue-browser-revoked'));
      return true;
    } catch {
      return false;
    }
  }, 'revocation barrier');
  stage = 'revocation';
  await detail.getByRole('button', {name: 'Refresh issue', exact: true}).click();
  await detail.getByText('This issue is not available to this account.', {exact: true}).waitFor();
  assert((await detail.locator('article').count()) === 0, 'revoked issue body remains');
  assert(
    (await page.locator('.soda-issue-factory-controls').count()) === 0 &&
      (await page.locator('soda-factory-work-journey button').count()) === 0,
    'revoked Factory facts or controls remain'
  );
  await save('issue-browser-result.json', {
    result: 'pass',
    actual_native_host_and_package: true,
    production_native_presentation: true,
    feed_to_issues_header: true,
    native_reader_login: true,
    shared_list_detail_conversation: true,
    sanitized_markdown: true,
    explicit_attachments: true,
    native_repository_and_explicit_branch_links: true,
    fragments_and_history: true,
    factory_section_and_back: true,
    recorded_factory_facts_read: true,
    deep_link_reload: true,
    native_original_available: true,
    read_only_before_grant: true,
    shared_comment_and_edit_readback: true,
    description_edit_readback: true,
    unsent_draft_retained_after_browser_restart: true,
    issue_closed_and_reopened: true,
    access_revocation_retired_facts: true,
    synthetic_api_or_admission: false,
    installed_release_qualified: false,
  });
  console.log(
    'PASS: real Fountain shared issue read, navigation, comment/edit readback and revoked access'
  );
} catch (error) {
  await save('issue-browser-error.json', {
    stage,
    error_class: error?.name ?? 'Error',
    ...(error?.check ? {check: error.check} : {}),
  });
  console.error('FAIL: native Fountain issue browser stage=' + stage);
  process.exitCode = 1;
} finally {
  await context.close();
}

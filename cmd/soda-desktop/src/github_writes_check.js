// Opt-in native acceptance; real UI actions in the owned disposable repository.
(async () => {
  const invoke = window.__TAURI__.core.invoke, fixture = __FIXTURE__;
  const assert = (ok, message) => { if (!ok) throw Error(message); };
  const until = async (check, label) => {
    const deadline = Date.now() + 90000;
    for (;;) {
      if (await check()) return;
      if (Date.now() > deadline) throw Error(label);
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  };
  const button = (root, name) => [...root.querySelectorAll('button')].find(node => node.textContent.trim() === name);
  const click = async (root, name) => {
    let node, refreshed = 0;
    await until(() => {
      const current = typeof root === 'string' ? document.querySelector(root) : root;
      node = current && button(current, name);
      if (node && !node.disabled) return true;
      const refresh = current && (button(current, 'Refresh review actions') || button(current, 'Refresh inline actions'));
      if (refresh && !refresh.disabled && Date.now() - refreshed > 5000) {
        refreshed = Date.now(); refresh.click();
      }
      return false;
    }, 'button unavailable: ' + name);
    node.click();
  };
  const input = (node, value) => { assert(node, 'input missing'); node.value = value; node.dispatchEvent(new Event('input', {bubbles: true, composed: true})); };
  const settled = async (tag, lookup) => {
    let last = 0;
    await until(() => {
      const component = document.querySelector(tag);
      if (!component) return false;
      const pending = button(component, lookup), busy = component.querySelector('[aria-busy]')?.getAttribute('aria-busy') === 'true';
      if (busy) return false;
      if (pending) {
        if (!pending.disabled && Date.now() - last > 5000) { last = Date.now(); pending.click(); }
        return false;
      }
      return true;
    }, 'saved command did not settle: ' + tag);
  };
  const write = async (tag, name, lookup, event) => {
    const component = document.querySelector(tag);
    assert(component, 'write component missing: ' + tag);
    let committed = false;
    component.addEventListener(event, () => { committed = true; }, {once: true});
    await click(component, name);
    await settled(tag, lookup);
    assert(committed, 'write did not report a committed outcome: ' + name);
  };
  let stage = 'connect';
  try {
    const account = document.querySelector('#factory-account');
    await until(() => [...account.options].some(row => row.value === __CONNECTION__), 'saved native account not displayed');
    account.value = __CONNECTION__; account.dispatchEvent(new Event('change', {bubbles: true}));
    const name = document.querySelector('#repository-name');
    await until(() => !name.disabled, 'repository discovery unavailable');
    name.value = fixture.name;
    document.querySelector('#repository-find').requestSubmit();
    await until(() => document.querySelector('#factory-repository').value === fixture.repository && document.querySelector('#connection-status').textContent.includes('Connected to'), 'fixture repository not connected');
    const repo = new URLSearchParams(location.hash.slice(1)).get('repository');
    assert(repo, 'canonical repository missing');
    const navigate = async tab => {
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab});
      await until(() => document.querySelector('soda-factory-pull')?.context?.initialTab === tab, 'PR route not mounted: ' + tab);
    };
    const read = async suffix => {
      const response = await document.querySelector('soda-factory-pull').context.transport.request('factory/collaboration/repositories/' + repo + '/pulls/' + fixture.number + suffix, {method: 'GET'});
      assert(response.ok, 'normal read refused: ' + suffix);
      return response.json();
    };
    if (fixture.summary_first && !fixture.resume_pending) {
      stage = 'summary-first';
      await navigate('reviews');
      await until(() => document.querySelector('soda-factory-pull-review textarea') && !document.querySelector('soda-factory-pull-review textarea').disabled, 'review editor unavailable');
      input(document.querySelector('soda-factory-pull-review textarea'), 'Native WebKit initial summary 茶');
      await write('soda-factory-pull-review', 'Save draft review', 'Check saved review', 'pull-review-saved');
    }
    stage = 'inline-draft';
    await navigate('files');
    const pull = await read('');
    assert(pull.head.commit === fixture.head && pull.ref.external_id === fixture.pull_id, 'owned PR fixture changed before writing');
    let diff;
    await until(() => { diff = [...document.querySelectorAll('soda-factory-diff')].find(node => node.file?.new_path === 'review.txt'); return diff?.shadowRoot && button(diff.shadowRoot, 'Comment on file'); }, 'owned file diff unavailable');
    const inlineBody = fixture.inline_body || 'Native WebKit file draft 茶';
    if (!fixture.resume_pending || fixture.append_pending) {
      await click(diff.shadowRoot, 'Comment on file');
      await until(() => diff.shadowRoot.querySelector('textarea'), 'inline editor missing');
      input(diff.shadowRoot.querySelector('textarea'), inlineBody);
      await write('soda-factory-pull-inline', 'Add to draft review', 'Check saved inline comment', 'pull-inline-saved');
    }
    await settled('soda-factory-pull-inline', 'Check saved inline comment');
    let pending;
    await until(async () => { pending = (await read('/reviews?limit=20')).items.find(row => row.state === 'pending' && row.author?.id === document.querySelector('soda-factory-pull').context.actor); return pending; }, 'own pending review not readable');
    const reviewID = pending.external_id;
    stage = 'pending-summary';
    await navigate('reviews');
    await until(() => document.querySelector('soda-factory-pull-review textarea') && !document.querySelector('soda-factory-pull-review textarea').disabled, 'review editor unavailable');
    let editor = document.querySelector('soda-factory-pull-review');
    if (fixture.summary_first && !fixture.resume_pending) {
      input(editor.querySelector('textarea'), 'Native WebKit summary 🍵');
      await write('soda-factory-pull-review', 'Save draft review', 'Check saved review', 'pull-review-saved');
      assert((await read('/reviews/' + reviewID)).body === 'Native WebKit summary 🍵', 'normal read lost edited summary');
      input(editor.querySelector('textarea'), '');
      await write('soda-factory-pull-review', 'Save draft review', 'Check saved review', 'pull-review-saved');
      assert((await read('/reviews/' + reviewID)).body === '', 'normal read lost explicit summary clear');
    } else {
      assert(button(editor, 'Save draft review').disabled, 'inline-only review offered an unsupported remote summary edit: ' + JSON.stringify((await read('/capabilities')).review_draft));
      input(editor.querySelector('textarea'), 'Published from native WebKit 茶');
      await navigate('files');
      await navigate('reviews');
      await until(() => document.querySelector('soda-factory-pull-review textarea')?.value === 'Published from native WebKit 茶', 'local inline-only summary lost on remount');
      editor = document.querySelector('soda-factory-pull-review');
    }
    input(editor.querySelector('textarea'), 'Published from native WebKit 茶');
    await write('soda-factory-pull-review', 'Submit comment', 'Check saved review', 'pull-review-saved');
    const submitted = await read('/reviews/' + reviewID);
    assert(submitted.state === 'commented' && submitted.body === 'Published from native WebKit 茶', 'normal publication readback failed');
    const threads = await read('/threads?limit=20');
    assert(threads.items.some(thread => thread.comments.items.some(row => row.comment.body === inlineBody)), 'published inline writing missing');
    stage = 'immediate-range';
    await navigate('files');
    await until(() => { diff = [...document.querySelectorAll('soda-factory-diff')].find(node => node.file?.new_path === 'review.txt'); return diff?.shadowRoot?.querySelector('.content')?.childElementCount; }, 'line renderer unavailable');
    let range;
    await until(() => { range = diff.shadowRoot.querySelector('form[aria-label="Select diff lines"]'); return range; }, 'line selection unavailable');
    range.querySelector('[name=side]').value = 'new';
    range.querySelector('[name=start]').value = '1'; range.querySelector('[name=end]').value = '2';
    range.requestSubmit();
    await click(diff.shadowRoot, 'Comment on selection');
    await until(() => diff.shadowRoot.querySelector('textarea'), 'range editor missing');
    input(diff.shadowRoot.querySelector('textarea'), 'Native WebKit immediate range 🌋');
    await write('soda-factory-pull-inline', 'Post inline comment', 'Check saved inline comment', 'pull-inline-saved');
    const published = await read('/threads?limit=20');
    assert(published.items.some(thread => thread.comments.items.some(row => row.comment.body === 'Native WebKit immediate range 🌋')), 'immediate range readback missing');
    stage = 'merge';
    const merge = document.querySelector('soda-factory-pull-merge');
    await until(() => merge.querySelector('input[aria-label="Merge message"]') && !merge.querySelector('input').disabled, 'merge eligibility unavailable');
    input(merge.querySelector('input[aria-label="Merge message"]'), 'Native WebKit fixture merge 茶');
    await write('soda-factory-pull-merge', 'Merge pull request', 'Check saved merge', 'pull-merge-saved');
    assert((await read('')).state === 'merged', 'ordinary merge readback missing');
    await invoke('report', {stage: 'github-writes-complete', error: null});
  } catch (error) {
    await invoke('report', {stage: 'failed', error: stage + ': ' + String(error)});
  }
})();

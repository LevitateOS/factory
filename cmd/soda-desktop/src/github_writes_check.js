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
        assert(!component.querySelector('[role=status]')?.textContent.includes('needs attention'), 'saved command needs attention: ' + tag);
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
    await until(() => document.querySelector('#factory-repository').value === fixture.repository && document.querySelector('#connection-status').textContent.includes('Connected to') ||
      /could not|unavailable/i.test(document.querySelector('#connection-status').textContent), 'fixture repository not connected');
    if (document.querySelector('#factory-repository').value !== fixture.repository || !document.querySelector('#connection-status').textContent.includes('Connected to')) {
      const session = await invoke('factory_prepare_connection', {connection: __CONNECTION__, repository: fixture.repository});
      let detail = 'unexpected direct connection';
      try { await invoke('factory_connect', {session}); } catch (error) { detail = String(error).slice(0, 240); }
      throw Error('fixture repository not connected: ' + detail);
    }
    const repo = new URLSearchParams(location.hash.slice(1)).get('repository');
    assert(repo, 'canonical repository missing');
    if (fixture.mode === 'placement') {
      stage = 'runtime-placement';
      location.hash = new URLSearchParams({view: 'issue', repository: repo, issue: String(fixture.issue_number)});
      await until(() => document.querySelector('soda-factory-issue')?.context?.issue === String(fixture.issue_number), 'placement fixture issue unavailable');
      const transport = document.querySelector('soda-factory-issue').context.transport;
      const path = 'repositories/' + repo + '/factory/placement';
      const get = async () => {
        const response = await transport.request(path, {method: 'GET'});
        assert(response.ok, 'placement read refused');
        return (await response.json()).placement;
      };
      assert(await get() === null, 'fresh fixture already has placement');
      const change = async (state, revision, expected = 200) => {
        const response = await transport.request(path, {method: 'PUT', headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({state, expected_revision: revision})});
        assert(response.status === expected, 'placement change status: ' + response.status);
        return response.ok ? (await response.json()).placement : undefined;
      };
      const controls = document.querySelector('#runtime-placement');
      await click(controls, 'Use this computer');
      await until(() => controls.textContent.includes('This computer · active · generation 1') && !button(controls, 'Retire controller')?.disabled, 'selected controller UI unavailable');
      let p = await get();
      assert(p.coordination === 'local_single_controller' && p.repository === repo && p.generation === '1', 'placement binding differs');
      await change('inactive', p.revision, 409);
      await click(controls, 'Retire controller');
      await until(() => controls.textContent.includes('This computer · retiring · generation 1'), 'retiring controller UI unavailable');
      await click(controls, 'Finish retirement');
      await until(() => controls.textContent.includes('No active controller.'), 'controller retirement did not finish');
      p = await get();
      const stale = p.revision;
      await click(controls, 'Use this computer');
      await until(() => controls.textContent.includes('This computer · active · generation 2'), 'new controller generation UI unavailable');
      p = await get();
      assert(p.generation === '2', 'placement generation did not advance');
      await change('retiring', stale, 409);
      await click(controls, 'Retire controller');
      await until(() => controls.textContent.includes('This computer · retiring · generation 2'), 'second controller retirement unavailable');
      await click(controls, 'Finish retirement');
      await until(() => controls.textContent.includes('No active controller.'), 'second controller retirement did not finish');
      p = await get();
      assert(JSON.stringify(await get()) === JSON.stringify(p), 'placement normal readback differs');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.checks_read_only) {
      stage = 'populated-checks';
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab: 'checks'});
      const detail = () => document.querySelector('soda-factory-pull');
      await until(() => detail()?.view?.pull?.head?.commit === fixture.head && detail()?.view?.busy === false,
        'public PR checks unavailable').catch(() => { throw Error('public PR checks unavailable: ' + JSON.stringify({
          route: location.hash, head: detail()?.view?.pull?.head?.commit, message: detail()?.view?.message,
          busy: detail()?.view?.busy, tab: detail()?.context?.initialTab,
        })); });
      await click(detail().querySelector('nav[aria-label="Check types"]'), 'Jobs');
      await until(() => detail()?.view?.busy === false && detail()?.view?.checks?.length > 0 &&
        detail()?.querySelector('section[aria-label="Checks"] button[aria-current="page"]')?.textContent.trim() === 'Jobs',
      'populated job checks unavailable');
      assert(detail().view.checks.every(row => row.name && row.state && row.conclusion) &&
        detail().querySelectorAll('section[aria-label="Checks"] li').length === detail().view.checks.length,
      'job checks did not render');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.issue_navigation) {
      stage = 'issue-navigation';
      assert(document.querySelector('soda-factory-feed'), 'connected feed unavailable');
      const nav = name => button(document.querySelector('soda-factory-app nav[aria-label="Factory"]'), name);
      nav('Issues').click();
      let row;
      await until(() => {
        row = [...document.querySelectorAll('soda-factory-issues li')].find(item =>
          item.querySelector('.soda-issue-meta')?.textContent.includes('#' + fixture.issue_number + ' ·'));
        return row?.querySelector('button.soda-issue-title:not([disabled])');
      }, 'fixture issue unavailable from feed');
      row.querySelector('button.soda-issue-title').click();
      const issue = () => document.querySelector('soda-factory-issue');
      await until(() => issue()?.context?.issue === String(fixture.issue_number) &&
        issue()?.querySelector('.soda-issue-detail h2')?.textContent === fixture.expected_title &&
        issue()?.textContent.includes(fixture.comment_body), 'fixture issue or comment unavailable');
      const editor = () => document.querySelector('soda-factory-issue-editor');
      if (fixture.issue_state) {
        await write('soda-factory-issue-state', 'Close issue', 'Check saved state', 'issue-state-saved');
        await until(() => /·\s*Closed\s*·/.test(issue()?.querySelector('.soda-issue-meta')?.textContent), 'issue did not close');
        await write('soda-factory-issue-state', 'Reopen issue', 'Check saved state', 'issue-state-saved');
        await until(() => /·\s*Open\s*·/.test(issue()?.querySelector('.soda-issue-meta')?.textContent), 'issue did not reopen');
      }
      if (!fixture.resume_pending) await click(editor(), 'Edit issue');
      await until(() => editor()?.querySelector('textarea[aria-label="Edit issue Markdown"]'), 'issue editor unavailable');
      if (fixture.resume_pending) assert(editor().querySelector('textarea[aria-label="Edit issue Markdown"]').value === fixture.draft_body,
        'issue draft lost after desktop restart');
      else input(editor().querySelector('textarea[aria-label="Edit issue Markdown"]'), fixture.draft_body);
      await until(() => Object.keys(localStorage).some(key => key.includes('issue') &&
        localStorage.getItem(key)?.includes(fixture.draft_body)), 'issue draft not saved');
      const issueRoute = location.hash;
      location.hash = new URLSearchParams({view: 'work-detail', repository: repo, issue: String(fixture.issue_number)});
      await until(() => new URLSearchParams(location.hash.slice(1)).get('view') === 'work-detail' &&
        document.querySelector('main[aria-label="Work detail"] soda-factory-work-detail section[aria-label="Factory work detail"]'),
      'Factory detail did not open');
      history.back();
      await until(() => location.hash === issueRoute && issue()?.context?.issue === String(fixture.issue_number) &&
        editor()?.querySelector('textarea[aria-label="Edit issue Markdown"]')?.value === fixture.draft_body,
      'Back did not restore the issue draft');
      nav('Feed').click();
      await until(() => document.querySelector('soda-factory-feed'), 'feed did not reopen');
      location.hash = new URLSearchParams({view: 'issue', repository: repo, issue: String(fixture.issue_number)});
      await until(() => issue()?.context?.issue === String(fixture.issue_number) &&
        editor()?.querySelector('textarea[aria-label="Edit issue Markdown"]')?.value === fixture.draft_body,
      'issue deep link lost the draft');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.outdated_thread) {
      stage = 'outdated-thread';
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab: 'threads'});
      const detail = () => document.querySelector('soda-factory-pull');
      await until(() => detail()?.view?.pull?.head?.commit === fixture.head && detail()?.view?.busy === false &&
        detail()?.view?.threads?.some(thread => thread.externalID === fixture.thread_id && thread.outdated === true),
      'outdated public discussion unavailable');
      assert(detail().querySelector('section[aria-label="Inline discussions"]')?.textContent.includes('Outdated'),
        'outdated discussion not labelled');
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab: 'files'});
      await until(() => detail()?.view?.files?.length === fixture.files && detail()?.view?.busy === false &&
        [...detail().querySelectorAll('soda-factory-diff')].some(node =>
          (node.file?.new_path === fixture.thread_path || node.file?.old_path === fixture.thread_path) &&
          [...(node.shadowRoot?.querySelectorAll('details.thread') || [])].some(thread =>
            /Original thread|Recorded thread/.test(thread.textContent) && thread.textContent.includes('Outdated'))),
      'outdated discussion missing from its original file');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.diff_edge) {
      stage = 'large-diff';
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab: 'files'});
      const detail = () => document.querySelector('soda-factory-pull');
      await until(() => detail()?.context?.pull === String(fixture.number) && detail()?.view?.files?.length === 10 &&
        detail()?.view?.diff?.head === fixture.head && detail()?.view?.busy === false,
      'large public PR first file page unavailable');
      let files = 0, pages = 0, renamed = 0, partial = 0;
      for (;;) {
        const view = detail().view;
        assert(view.pull?.head?.commit === fixture.head && view.diff?.head === fixture.head, 'large PR changed while paging');
        files += view.files.length;
        renamed += view.files.filter(file => file.change === 'renamed').length;
        partial += view.files.filter(file => file.patch_state !== 'complete').length;
        pages++;
        if (files >= 100 && renamed > 0 && partial > 0) break;
        assert(view.next && pages < 25, 'large PR edge files unavailable');
        const first = view.files[0].new_path || view.files[0].old_path;
        await click(detail().querySelector('nav[aria-label="Pull request pages"]'), 'Next page');
        await until(() => detail()?.view?.busy === false && detail()?.view?.files?.length > 0 &&
          (detail().view.files[0].new_path || detail().view.files[0].old_path) !== first,
        'large PR next file page unavailable');
      }
      const shown = detail().querySelectorAll('soda-factory-diff');
      assert(shown.length === detail().view.files.length, 'large PR file page not rendered');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.pull_read_only) {
      stage = 'pull-list';
      location.hash = new URLSearchParams({view: 'pulls', repository: repo});
      let row;
      await until(() => {
        row = [...document.querySelectorAll('soda-factory-pulls li')].find(item =>
          item.querySelector('.soda-pull-meta')?.textContent.includes('#' + fixture.number + ' ·'));
        return row?.querySelector('button.soda-pull-title:not([disabled])');
      }, 'owned PR absent from shared list');
      row.querySelector('button.soda-pull-title').click();
      await until(() => document.querySelector('soda-factory-pull')?.context?.pull === String(fixture.number) &&
        document.querySelector('soda-factory-pull .soda-pull-detail h2')?.textContent === fixture.title &&
        document.querySelector('soda-factory-pull .soda-pull-detail')?.getAttribute('aria-busy') === 'false',
      'owned PR detail unavailable');
      const pull = document.querySelector('soda-factory-pull');
      const response = await pull.context.transport.request('factory/collaboration/repositories/' + repo + '/pulls/' + fixture.number, {method: 'GET'});
      assert(response.ok && (await response.json()).head.commit === fixture.head, 'owned PR head changed before read check');
      const tab = async (label, name) => {
        await click(document.querySelector('soda-factory-pull nav[aria-label="Pull request sections"]'), label);
        await until(() => {
          const current = document.querySelector('soda-factory-pull');
          return new URLSearchParams(location.hash.slice(1)).get('tab') === name &&
            current?.context?.initialTab === name && current?.view?.pull?.ref?.number === String(fixture.number) &&
            current?.view?.busy === false;
        },
        'PR section unavailable: ' + name);
      };
      stage = 'pull-conversation';
      assert(pull.querySelector('section[aria-label="Conversation"] ol li'), 'conversation comment missing');
      stage = 'pull-commits';
      await tab('Commits', 'commits');
      assert(document.querySelector('soda-factory-pull section[aria-label="Commits"] ol li'), 'PR commit missing');
      stage = 'pull-files';
      await tab('Files', 'files');
      await until(() => document.querySelector('soda-factory-diff')?.file?.new_path === 'review.txt', 'changed file missing');
      stage = 'pull-reviews';
      await tab('Reviews', 'reviews');
      const reviewView = document.querySelector('soda-factory-pull').view;
      assert(reviewView.reviews.some(review => review.state === 'commented'),
        'submitted review missing: ' + JSON.stringify({states: reviewView.reviews.map(review => review.state), message: reviewView.message}));
      stage = 'pull-threads';
      await tab('Inline discussions', 'threads');
      assert(document.querySelector('soda-factory-pull section[aria-label="Inline discussions"] > ol > li'), 'inline discussion missing');
      stage = 'pull-checks';
      await tab('Checks', 'checks');
      const checks = document.querySelector('soda-factory-pull section[aria-label="Checks"]');
      assert(checks?.textContent.includes('Status gate:') && checks.querySelector('button[aria-current="page"]')?.textContent.trim() === 'Statuses', 'status checks missing');
      await click(checks, 'Jobs');
      await until(() => document.querySelector('soda-factory-pull section[aria-label="Checks"] button[aria-current="page"]')?.textContent.trim() === 'Jobs' &&
        document.querySelector('soda-factory-pull .soda-pull-detail')?.getAttribute('aria-busy') === 'false', 'job checks unavailable');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.issue_only) {
      stage = 'issue-list';
      location.hash = new URLSearchParams({view: 'issues', repository: repo});
      let issueRow;
      await until(() => {
        issueRow = [...document.querySelectorAll('soda-factory-issues li')].find(row =>
          row.querySelector('.soda-issue-meta')?.textContent.includes('#' + fixture.issue_number + ' ·'));
        return issueRow?.querySelector('button.soda-issue-title:not([disabled])');
      }, 'owned issue absent from shared list');
      issueRow.querySelector('button.soda-issue-title').click();
      await until(() => document.querySelector('soda-factory-issue')?.context?.issue === String(fixture.issue_number) &&
        document.querySelector('soda-factory-issue article[aria-label="Issue description"]')?.textContent.includes(fixture.expected_body),
      'owned issue detail unavailable');
      const detail = document.querySelector('soda-factory-issue');
      assert(detail.querySelector('.soda-issue-detail h2').textContent === fixture.expected_title, 'owned issue changed before editing');
      const readIssue = async suffix => {
        const response = await detail.context.transport.request('factory/collaboration/repositories/' + repo + '/issues/' + fixture.issue_number + suffix, {method: 'GET'});
        assert(response.ok, 'normal issue read refused: ' + suffix);
        return response.json();
      };
      stage = 'issue-edit';
      const editor = document.querySelector('soda-factory-issue-editor');
      await click(editor, 'Edit issue');
      await until(() => editor.querySelector('input[aria-label="Edit issue title"]') && editor.querySelector('textarea[aria-label="Edit issue Markdown"]'), 'shared issue editor unavailable');
      input(editor.querySelector('input[aria-label="Edit issue title"]'), fixture.edited_title);
      input(editor.querySelector('textarea[aria-label="Edit issue Markdown"]'), fixture.edited_body);
      await write('soda-factory-issue-editor', 'Save issue', 'Check edit status', 'issue-edit-saved');
      await until(async () => { const issue = await readIssue(''); return issue.title === fixture.edited_title && issue.body === fixture.edited_body; }, 'edited issue not readable');
      await until(() => detail.querySelector('.soda-issue-detail h2')?.textContent === fixture.edited_title &&
        detail.querySelector('article[aria-label="Issue description"]')?.textContent.includes(fixture.edited_body), 'edited issue not rendered');
      stage = 'issue-comment';
      const composer = document.querySelector('soda-factory-issue-comment');
      await until(() => composer?.querySelector('textarea[aria-label="Comment Markdown"]'), 'issue comment composer unavailable');
      input(composer.querySelector('textarea[aria-label="Comment Markdown"]'), fixture.comment_body);
      await write('soda-factory-issue-comment', 'Post comment', 'Check comment status', 'issue-comment-saved');
      await until(async () => (await readIssue('/comments?limit=20')).items?.some(row => row.comment?.body === fixture.comment_body), 'posted issue comment not readable');
      location.hash = new URLSearchParams({view: 'issues', repository: repo});
      await until(() => document.querySelector('soda-factory-issues'), 'issue list did not remount');
      location.hash = new URLSearchParams({view: 'issue', repository: repo, issue: String(fixture.issue_number)});
      await until(() => document.querySelector('soda-factory-issue .soda-issue-detail h2')?.textContent === fixture.edited_title &&
        document.querySelector('soda-factory-issue article[aria-label="Issue description"]')?.textContent.includes(fixture.edited_body), 'edited issue lost after navigation');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    const navigate = async tab => {
      location.hash = new URLSearchParams({view: 'pull', repository: repo, pull: String(fixture.number), tab});
      await until(() => document.querySelector('soda-factory-pull')?.context?.initialTab === tab, 'PR route not mounted: ' + tab);
    };
    const read = async suffix => {
      const response = await document.querySelector('soda-factory-pull').context.transport.request('factory/collaboration/repositories/' + repo + '/pulls/' + fixture.number + suffix, {method: 'GET'});
      assert(response.ok, 'normal read refused: ' + suffix);
      return response.json();
    };
    if (fixture.mode === 'branch_rule') {
      stage = 'protected-branch-merge';
      await navigate('reviews');
      const pull = await read('');
      assert(pull.ref.external_id === fixture.pull_id && pull.head.commit === fixture.head && pull.state === 'open', 'protected fixture changed');
      const merge = () => document.querySelector('soda-factory-pull-merge');
      await until(() => merge()?.querySelector('button') && merge()?.querySelector('section[aria-label="Merge pull request"]')?.getAttribute('aria-busy') === 'false', 'merge admission unavailable');
      const caps = await read('/capabilities');
      assert(['merge', 'squash', 'rebase'].every(method => caps.merge_methods?.[method]?.state !== 'allowed') &&
        button(merge(), 'Merge pull request')?.disabled, 'protected branch offered a merge');
      const command = crypto.randomUUID().replaceAll('-', '');
      const path = 'factory/collaboration/repositories/' + repo + '/pulls/' + fixture.number + '/merges';
      const response = await merge().context.transport.request(path, {method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({command_id: command, method: 'merge', message: 'Protected branch refusal fixture', expected_head: fixture.head})});
      assert(response.status === 403, 'protected merge was captured or returned an unexpected status');
      const lookup = await merge().context.transport.request(path + '/commands/' + command, {method: 'GET'});
      assert(lookup.status === 404, 'protected merge captured a command');
      const after = await read('');
      assert(after.ref.external_id === fixture.pull_id && after.state === 'open' && after.head.commit === fixture.head,
        'protected pull changed after denied merge');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.mode === 'merge_only') {
      stage = 'merge-' + fixture.merge_method;
      assert(['squash', 'rebase'].includes(fixture.merge_method), 'unsupported merge fixture');
      await navigate('reviews');
      const pull = await read('');
      assert(pull.ref.external_id === fixture.pull_id && pull.head.commit === fixture.head && pull.state === 'open', 'owned merge fixture changed');
      const control = () => document.querySelector('soda-factory-pull-merge');
      let select;
      await until(() => {
        select = control()?.querySelector('select[aria-label="Merge method"]');
        return select && !select.disabled && [...select.options].some(row => row.value === fixture.merge_method);
      }, 'selected merge method unavailable');
      assert([...select.options].every(row => ['merge', 'squash', 'rebase'].includes(row.value)), 'unsupported GitHub merge method offered');
      select.value = fixture.merge_method; select.dispatchEvent(new Event('change', {bubbles: true}));
      await until(() => (control().querySelector('textarea[aria-label="Merge message"]') !== null) === (fixture.merge_method === 'squash'), 'merge message mode did not change');
      if (fixture.merge_method === 'squash') {
        input(control().querySelector('textarea[aria-label="Merge message"]'), fixture.merge_message);
        assert(control().querySelector('textarea').value === fixture.merge_message && fixture.merge_message.includes('\n\n'), 'merge body lost in editor');
      }
      await write('soda-factory-pull-merge', 'Merge pull request', 'Check saved merge', 'pull-merge-saved');
      assert((await read('')).state === 'merged', 'selected merge readback failed');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.mode === 'reviewer_requests') {
      stage = 'reviewer-requests';
      await navigate('reviews');
      const control = () => document.querySelector('soda-factory-pull-reviewers');
      await until(() => control()?.querySelector('input[name=name]'), 'reviewer lookup unavailable');
      const kind = fixture.reviewer_kind || 'user';
      assert(kind === 'user' || kind === 'team', 'invalid fixture reviewer kind');
      control().querySelector('select[name=kind]').value = kind;
      input(control().querySelector('input[name=name]'), fixture.reviewer);
      await click(control(), 'Find reviewer');
      const request = 'Request review from ' + fixture.reviewer;
      await until(() => button(control(), request), 'reviewer lookup did not return the second account');
      if (fixture.expect_unavailable) {
        assert(kind === 'team' && button(control(), request).disabled &&
          control().querySelector('[role=status]').textContent === 'Team review requests are unavailable for this repository.', 'unsupported team request was offered');
        const target = await read('/review-requests/lookup?kind=team&name=' + encodeURIComponent(fixture.reviewer));
        const command = crypto.randomUUID().replaceAll('-', '');
        const path = 'factory/collaboration/repositories/' + repo + '/pulls/' + fixture.number + '/review-requests';
        const refused = await control().context.transport.request(path, {method: 'POST', headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({command_id: command, reviewer_kind: 'team', reviewer_id: target.team.id, requested: true})});
        assert(refused.status === 403, 'unsupported team request admitted');
        const lookup = await control().context.transport.request(path + '/commands/' + command, {method: 'GET'});
        assert(lookup.status === 404, 'unsupported team request captured a command');
        await invoke('report', {stage: 'github-writes-complete', error: null});
        return;
      }
      const change = async (label, requested) => {
        await click(control(), label);
        await settled('soda-factory-pull-reviewers', 'Check saved reviewer change');
        assert(control().querySelector('[role=status]').textContent === 'Reviewer request changed.', 'reviewer command did not commit');
        const result = await read('/review-requests');
        assert(result.items.some(item => item.reviewer?.label === fixture.reviewer && item.reviewer?.kind === kind &&
          (kind === 'user' ? item.reviewer.actor : item.reviewer.team)) === requested,
          'reviewer change readback failed');
      };
      await change(request, true);
      await change('Remove request for ' + fixture.reviewer, false);
      await change(request, true);
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.mode === 'other_actor') {
      stage = 'other-actor-review';
      await navigate('reviews');
      if (fixture.expect_saved_reviewer) {
        await until(() => document.querySelector('soda-factory-pull-reviewers [role=status]')?.textContent.includes('needs attention'), 'uncertain reviewer outcome was hidden');
        assert(button(document.querySelector('soda-factory-pull-reviewers'), 'Check saved reviewer change'), 'uncertain reviewer command lost');
      }
      const pull = await read('');
      const actor = document.querySelector('soda-factory-pull').context.actor;
      assert(pull.ref.external_id === fixture.pull_id && pull.head.commit === fixture.head && pull.author && pull.author.id !== actor,
        'other-actor fixture identity changed');
      const body = 'Factory second-account private summary 茶';
      const inline = 'Factory second-account private file comment 茶';
      if (fixture.review_stage === 'draft') {
        await until(() => document.querySelector('soda-factory-pull-review textarea:not([disabled])'), 'review editor unavailable');
        input(document.querySelector('soda-factory-pull-review textarea'), body);
        await write('soda-factory-pull-review', 'Save draft review', 'Check saved review', 'pull-review-saved');
        await navigate('files');
        let diff;
        await until(() => { diff = [...document.querySelectorAll('soda-factory-diff')].find(node => node.file?.new_path === 'review.txt'); return diff?.shadowRoot && button(diff.shadowRoot, 'Comment on file'); }, 'owned file unavailable');
        await click(diff.shadowRoot, 'Comment on file');
        await until(() => diff.shadowRoot.querySelector('textarea'), 'file comment editor unavailable');
        input(diff.shadowRoot.querySelector('textarea'), inline);
        await write('soda-factory-pull-inline', 'Add to draft review', 'Check saved inline comment', 'pull-inline-saved');
      } else assert(['verdicts', 'publication'].includes(fixture.review_stage), 'unknown other-actor review stage');
      const pending = (await read('/reviews?limit=20')).items.find(row => row.state === 'pending' && row.author?.id === actor);
      assert(pending?.body === body, 'own private summary missing');
      const pendingThreads = await read('/threads?limit=20');
      assert(pendingThreads.items.some(thread => thread.comments.items.some(row => row.comment.body === inline)), 'own private file comment missing');
      if (fixture.review_stage !== 'draft') {
        await navigate('reviews');
        await until(() => document.querySelector('soda-factory-pull-review textarea:not([disabled])'), 'retained review editor unavailable');
        input(document.querySelector('soda-factory-pull-review textarea'), body);
        const comment = fixture.review_stage === 'publication';
        await write('soda-factory-pull-review', comment ? 'Submit comment' : 'Approve', 'Check saved review', 'pull-review-saved');
        const approved = await read('/reviews/' + pending.external_id);
        assert(approved.state === (comment ? 'commented' : 'approved') && approved.author.id === actor && approved.body === body, 'publication readback failed');
        if (!comment) {
          input(document.querySelector('soda-factory-pull-review textarea'), 'Factory second-account changes requested 茶');
          await write('soda-factory-pull-review', 'Request changes', 'Check saved review', 'pull-review-saved');
          assert((await read('/reviews?limit=20')).items.some(row => row.state === 'changes_requested' && row.author?.id === actor &&
            row.body === 'Factory second-account changes requested 茶'), 'request-changes readback failed');
        }
      }
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
    if (fixture.mode === 'thread_resolution') {
      stage = 'merged-thread-resolution';
      await navigate('threads');
      const pull = await read('');
      assert(pull.ref.external_id === fixture.pull_id && pull.state === 'merged', 'owned merged PR changed');
      let control;
      await until(() => {
        control = [...document.querySelectorAll('soda-factory-thread-resolution')]
          .find(node => node.context?.threadID === fixture.thread_id);
        return control && button(control, 'Manage resolution');
      }, 'owned thread control missing');
      control.setAttribute('data-owned-thread-check', '');
      const selector = 'soda-factory-thread-resolution[data-owned-thread-check]';
      const state = async () => (await read('/threads?limit=20')).items
        .find(row => row.external_id === fixture.thread_id)?.resolution;
      assert(await state() === 'unresolved', 'owned thread must start unresolved');
      await click(control, 'Manage resolution');
      await write(selector, 'Resolve discussion', 'Check saved thread change', 'pull-thread-saved');
      assert(await state() === 'resolved', 'merged thread resolution readback failed');
      await click(selector, 'Manage resolution');
      await write(selector, 'Reopen discussion', 'Check saved thread change', 'pull-thread-saved');
      assert(await state() === 'unresolved', 'merged thread reopen readback failed');
      await invoke('report', {stage: 'github-writes-complete', error: null});
      return;
    }
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
    if (fixture.summary_first && (!fixture.resume_pending || fixture.edit_summary)) {
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
    await until(() => merge.querySelector('textarea[aria-label="Merge message"]') && !merge.querySelector('textarea').disabled, 'merge eligibility unavailable');
    input(merge.querySelector('textarea[aria-label="Merge message"]'), 'Native WebKit fixture merge 茶');
    await write('soda-factory-pull-merge', 'Merge pull request', 'Check saved merge', 'pull-merge-saved');
    assert((await read('')).state === 'merged', 'ordinary merge readback missing');
    await invoke('report', {stage: 'github-writes-complete', error: null});
  } catch (error) {
    await invoke('report', {stage: 'failed', error: stage + ': ' + String(error)});
  }
})();

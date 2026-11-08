// Existing opt-in native check: production UI and a marked, owned GitHub fixture.
(async () => {
  const invoke = window.__TAURI__.core.invoke, fixture = __FIXTURE__;
  const planningPaths = fixture.source_paths ?? ['README.md', 'q07/input.md'];
  const assert = (value, message) => { if (!value) throw Error(message); };
  let stage = 'connect';
  const until = async (check, label) => {
    const deadline = Date.now() + 90000;
    while (!await check()) {
      assert(Date.now() < deadline, stage + ': ' + label);
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  };
  const button = (root, name) => [...root.querySelectorAll('button')].find(node => node.textContent.trim() === name);
  const click = async (root, name) => {
    let node;
    await until(() => { node = button(root, name); return node && !node.disabled; }, name);
    node.click();
  };
  const input = (node, value) => {
    assert(node && !node.disabled, 'input unavailable');
    node.value = value;
    node.dispatchEvent(new Event(node.tagName === 'SELECT' ? 'change' : 'input', {bubbles: true}));
  };
  try {
    const runtime = await invoke('runtime_status');
    assert(!runtime.execution_error, 'native execution unavailable: ' + runtime.execution_error);
    const account = document.querySelector('#factory-account');
    await until(() => [...account.options].some(row => row.value === __CONNECTION__), 'saved account');
    input(account, __CONNECTION__);
    const name = document.querySelector('#repository-name');
    await until(() => !name.disabled, 'repository discovery');
    input(name, fixture.name);
    document.querySelector('#repository-find').requestSubmit();
    await until(() => document.querySelector('#factory-repository').value === fixture.repository &&
      document.querySelector('#connection-status').textContent.includes('Connected to'), 'connected fixture');
    const app = document.querySelector('soda-factory-app');
    await click(app.querySelector('nav[aria-label="Factory"]'), 'Work');
    await until(() => app.querySelector('soda-factory-role-settings')?.querySelector('[aria-label="Coding model"]'), 'role settings');
    const roles = app.querySelector('soda-factory-role-settings'), context = roles.current;
    const base = `repositories/${context.repository}/factory`;
    let approvalReply = '', selectionReply = '', loseReply = fixture.drop_approval_reply === true;
    const request = context.transport.request.bind(context.transport);
    context.transport.request = async (path, options) => {
      const response = await request(path, options);
      if (path === base + '/setup' && options?.method === 'POST')
        selectionReply = response.status + (response.ok ? '' : ' ' + (await response.clone().text()).slice(0, 1000));
      if (path === base + '/setup' && options?.method === 'PUT') {
        approvalReply = response.status + ' ' + (await response.clone().text()).slice(0, 1000);
        if (response.ok && loseReply) { loseReply = false; throw Error('Native check dropped the completed approval reply'); }
      }
      return response;
    };
    await invoke('report', {stage: 'onboarding-repository:' + context.repository, error: null});
    const api = async (path = base) => {
      const response = await context.transport.request(path, {method: 'GET'});
      assert(response.ok, 'read failed: ' + path + ' ' + response.status);
      return response.json();
    };
    const roleInput = (label, value) => input(roles.querySelector(`[aria-label="${label}"]`), value);
    const roleClick = async name => { await until(() => !roles.busy, 'roles idle'); await click(roles, name); await until(() => !roles.busy, name + ' settled'); };
    const initial = await api();
    if (!fixture.reopen) {
    assert(initial.policy === null && initial.sponsorships.length === 0, 'fresh profile invented policy/funding');
    stage = 'disabled policy and placement';
    roleInput('Coding model', 'muse-spark-1.3-contributor');
    roleInput('Review model', 'muse-spark-1.3-contributor');
    roleInput('Automation target branch', fixture.target);
    roleInput('Required CI checks', 'fixture/check');
    roleInput('Automation merge method', 'squash');
    const policyConsent = [...roles.querySelectorAll('input[type=checkbox]')].find(node => node.closest('label')?.textContent.includes('for repository automation'));
    assert(policyConsent && !policyConsent.checked, 'policy account consent');
    policyConsent.click();
    await roleClick('Save disabled automation policy');
    assert((await api()).policy?.enabled === false, 'policy not saved disabled: ' + roles.message);
    await click(document, 'Use this computer');
    await until(async () => (await api(base + '/placement')).placement?.state === 'active', 'active placement');
    await click(document, 'Allow isolated workspaces');
    await until(async () => (await api()).environment_grant?.active === true, 'workspace grant');
    }
    const setup = document.querySelector('soda-factory-workspace-setup');
    await click(setup, 'Refresh workspace setup');
    await until(() => !setup.busy && setup.view?.available, 'setup available');
    if (fixture.reopen) {
      stage = 'retained approval recovery';
      if (fixture.resume_review) {
        assert(!setup.command && setup.view.preparations.every(row => !row.current), 'settled approval or historical observations changed');
      } else {
      assert(initial.policy?.enabled === false && setup.command && setup.view.preparations.length === 2 &&
        setup.view.preparations.every(row => !row.current), 'reopen lost pending approval or invented current readiness');
      const changed = await context.transport.request(base + '/setup', {method: 'PUT', body: JSON.stringify({
        command_id: setup.command.command_id, ...setup.command.payload, target: 'refs/heads/main',
      })});
      assert(changed.status === 409 && (await changed.json()).error.code === 'command_conflict', 'changed approval command was not refused exactly');
      const recovery = button(setup, 'Retry saved approval');
      const consent = recovery.closest('form').querySelector('input[type=checkbox]');
      assert(consent && !consent.checked, 'recovered approval consent default'); consent.click();
      await click(setup, 'Retry saved approval');
      await until(() => !setup.busy, 'retained receipt read settled');
      assert(!setup.command && setup.view.preparations.every(row => !row.current),
        'pending receipt did not settle without replay: ' + approvalReply + ' ' + setup.message);
      assert(!setup.querySelector('form input[type=checkbox]').checked, 'changed source retained old consent');
      }
    } else {
      assert(!setup.view.approval && !setup.view.requirement && setup.view.preparations.length === 0, 'fresh setup seeded');
    }
    assert(setup.view.source === fixture.source, 'fixture branch source differs');
    const approve = () => button(setup, 'Approve and prepare both roles');
    const paths = () => setup.querySelector('textarea[name=paths]');
    const select = async selection => {
      input(paths(), selection.join('\n'));
      await setup.updateComplete;
      assert(approve().disabled, 'editing paths retained approval authority');
      await click(setup, 'Load selected files');
      await until(() => !setup.busy, 'selection read settled');
      await setup.updateComplete;
    };
    stage = 'selected-file bounds and consent';
    const bounds = Array.from({length: 65}, (_, i) => `q07/bounds/${String(i).padStart(2, '0')}.txt`);
    if (!fixture.skip_bounds) {
    await select(bounds);
    assert(setup.message.includes('at most 64') && approve().disabled, 'path-count bound reused old consent');
    for (const invalid of [['../README.md'], ['q07/oversize.txt'], bounds.slice(0, 5)]) {
      await select(invalid);
      assert(setup.message && approve().disabled && !setup.selectionCurrent, 'invalid selection enabled old approval: ' + invalid);
    }
    await select(['q07/missing.txt']);
    assert(setup.view.missing_paths.length === 1 && approve().disabled, 'missing source accepted');
    await select(bounds.slice(0, 4));
    assert(setup.selectionCurrent && Object.keys(setup.view.source_inputs).length === 4 &&
      Object.values(setup.view.source_files).reduce((bytes, value) => bytes + new TextEncoder().encode(value).length, 0) === 128 * 1024,
      'exact aggregate selection bound refused');
    }
    await select(planningPaths);
    assert(setup.selectionCurrent && Object.keys(setup.view.source_inputs).length === planningPaths.length,
      'reviewed planning inputs: ' + selectionReply + ' ' + setup.message + '; missing: ' + setup.view.missing_paths.join(', '));
    let consent = setup.querySelector('form input[type=checkbox]');
    consent.click();
    input(paths(), 'README.md');
    await setup.updateComplete;
    assert(approve().disabled && !setup.querySelector('form input[type=checkbox]').checked, 'changed selection kept consent');
    await select(planningPaths);
    stage = 'actual native preparation';
    input(setup.querySelector('[name="file:setup.sh"]'), '#!/bin/bash\nset -eu\nmkdir -p .factory-build\nprintf "reviewed Q07 setup\\n" > .factory-build/ready\n');
    input(setup.querySelector('[name="file:check.sh"]'), '#!/bin/bash\nset -eu\ntest -s .factory-build/ready\ntest -s q07/input.md\n');
    input(setup.querySelector('[name=network]'), 'none');
    input(setup.querySelector('[name=setup_seconds]'), '10');
    input(setup.querySelector('[name=check_seconds]'), '10');
    input(setup.querySelector('[name=tools]'), 'bash git');
    consent = setup.querySelector('form input[type=checkbox]');
    assert(consent && !consent.checked, 'new selection consent default');
    consent.click();
    await click(setup, 'Approve and prepare both roles');
    await until(() => !setup.busy, 'approval settled');
    if (fixture.drop_approval_reply)
      assert(setup.command && approvalReply.startsWith('200 '), 'lost reply was not retained: ' + approvalReply);
    else
      assert(!setup.command && setup.view.approval && setup.view.requirement, 'approval not confirmed: ' + setup.message + ' ' + approvalReply);
    await until(async () => {
      await click(setup, 'Refresh workspace setup');
      await until(() => !setup.busy, 'native observation settled');
      return setup.view.preparations.length === 2 && setup.view.preparations.every(row => row.current && row.state.ready);
    }, 'both actual role workspaces ready: ' + setup.message);
    if (fixture.preview_issue) {
      stage = 'connected acceptance preview';
      location.hash = new URLSearchParams({view: 'work-detail', repository: context.repository, issue: String(fixture.issue_number)});
      let acceptance;
      await until(() => {
        acceptance = document.querySelector('soda-factory-requirements-acceptance');
        return acceptance?.allowed && acceptance.ready && !acceptance.busy;
      }, 'current shared work observation and acceptance authority');
      await click(acceptance, 'Review current requirements');
      await until(() => !acceptance.busy, 'native acceptance preview settled');
      assert(acceptance.preview?.repository === context.repository && acceptance.preview.issue === String(fixture.issue_number),
        'connected issue preview unavailable: ' + acceptance.message);
      assert(!acceptance.reviewed && button(acceptance, 'Accept requirements')?.disabled, 'preview invented owner acceptance');
    }
    assert((await api()).policy.enabled === false, 'setup started automation');
    await invoke('report', {stage: 'github-writes-complete', error: null});
  } catch (error) {
    await invoke('report', {stage: 'failed', error: stage + ': ' + String(error)});
  }
})();

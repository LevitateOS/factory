(async () => {
  const invoke = window.__TAURI__.core.invoke;
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  let waiting = '';
  const until = async (check, why) => {
    waiting = why;
    const end = Date.now() + 45000;
    while (!await check()) {
      assert(Date.now() < end, 'Owner policy deadline: ' + why + '; ' + document.querySelector('soda-desktop-placement')?.textContent);
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
  };
  const button = (root, text) => [...root.querySelectorAll('button')].find(node => node.textContent.trim() === text);
  const input = (root, label, value) => {
    const node = root.querySelector(`[aria-label="${label}"]`);
    assert(node && !node.disabled, 'Input unavailable: ' + label);
    node.value = value;
    node.dispatchEvent(new Event(node.tagName === 'SELECT' ? 'change' : 'input', {bubbles: true}));
  };
  try {
    await until(() => [...document.querySelector('#factory-account').options].some(o => o.value === __CONNECTION__), 'saved fixture account');
    const account = document.querySelector('#factory-account');
    account.value = __CONNECTION__; account.dispatchEvent(new Event('change', {bubbles: true}));
    const repository = document.querySelector('#repository-name');
    await until(() => !repository.disabled && [...repository.list.options].some(o => o.value === 'owned/repo'), 'observed repository');
    repository.value = 'owned/repo'; document.querySelector('#repository-find').requestSubmit();
    await until(() => document.querySelector('#connection-status').textContent === 'Connected to owned/repo.', 'native product binding');
    const app = document.querySelector('soda-factory-app');
    button(app.querySelector('nav[aria-label="Factory"]'), 'Work').click();
    await until(() => app.querySelector('soda-factory-role-settings')?.querySelector('[aria-label="Coding model"]'), 'shared settings');
    const roles = app.querySelector('soda-factory-role-settings');
    assert(!roles.querySelector('soda-factory-workspace-setup'), 'role settings eagerly opened workspace source reads');
    const context = roles.current, path = `repositories/${context.repository}/factory`;
    const api = async (method, target, body) => {
      const response = await context.transport.request(target, {method, ...(body === undefined ? {} : {body: JSON.stringify(body)})});
      return {status: response.status, body: await response.json()};
    };
    const status = async () => {
      const response = await api('GET', path); assert(response.status === 200, `settings read (${waiting}, HTTP ${response.status})`); return response.body;
    };
    const idle = () => !roles.busy;
    const click = async text => { await until(() => idle() && button(roles, text) && !button(roles, text).disabled, text); button(roles, text).click(); await until(idle, text + ' joined'); };
    const before = await status();
    assert(before.policy === null && before.sponsorships.length === 0, 'setup invented policy/funding');
    const source = before.spending_sources.find(s => s.label === 'This Muse login');
    assert(source && source.native.authentication === 'muse_login' && !source.native.credential_sha256, 'CLI-owned source');
    const sourceID = source.connection, model = 'muse-spark-1.3-contributor';
    assert(button(roles, 'Enroll automation policy') === undefined, 'initial policy enrolled automatically');
    input(roles, 'Coding model', model); input(roles, 'Review model', model);
    assert(!roles.querySelector('[aria-label="Judgments model"]') && !roles.querySelector('[aria-label="Judgments login"]'), 'judgments are not a Muse login role');
    input(roles, 'Automation target branch', before.policy_options.target_branch);
    input(roles, 'Required CI checks', 'fixture/check');
    assert(before.policy_options.merge_methods.includes('squash'), 'observed squash option');
    input(roles, 'Automation merge method', 'squash');
    input(roles, 'Time limit per run in minutes', '15');
    input(roles, 'Maximum independent reviews per attempt', '2');
    input(roles, 'Maximum corrections per attempt', '1');
    const consent = [...roles.querySelectorAll('input[type=checkbox]')].find(node => node.closest('label')?.textContent.includes('for repository automation'));
    assert(consent && !consent.checked, 'account consent default'); consent.click();
    await click('Save disabled automation policy');
    const initial = await status();
    assert(initial.policy.enabled === false && !initial.policy.judgments && !initial.policy.roles['soda-coder'].account && !initial.policy.roles['soda-reviewer'].account, 'disabled bootstrap invented spending choices');
    assert(button(roles, 'Enroll automation policy').disabled, 'unfunded enrollment available');
    // Local resource settings remain separate from collaboration write/admin.
    const runLimit = button(document, 'Save run limit');
    await until(() => runLimit && !runLimit.disabled, 'local capacity control');
    const limit = [...document.querySelectorAll('label')].find(node => node.textContent.includes('Maximum simultaneous agent runs'))?.querySelector('input');
    assert(limit, 'run limit input'); limit.value = '2'; runLimit.click();
    await until(async () => (await status()).capacity?.max_concurrent_runs === 2, 'capacity readback');
    await until(() => button(document.querySelector('soda-desktop-placement'), 'Use this computer') && !button(document.querySelector('soda-desktop-placement'), 'Use this computer').disabled, 'controller observation settled');
    button(document.querySelector('soda-desktop-placement'), 'Use this computer').click();
    await until(async () => (await api('GET', `${path}/placement`)).body.placement?.state === 'active', 'controller readback');
    await until(() => button(document, 'Allow this repository to use this computer') && !button(document, 'Allow this repository to use this computer').disabled, 'current local consent');
    const permission = button(document, 'Allow this repository to use this computer');
    assert(permission && !permission.disabled, 'local repository consent'); permission.click();
    await until(async () => (await status()).operator_grant?.active === true, 'operator consent readback');
    const offered = () => [...roles.querySelector('[aria-label="Coding login"]').options].some(option => option.value === sourceID && !option.disabled);
    await click('Refresh roles');
    assert(!offered(), 'login offered without workspace permission');
    await until(() => button(document, 'Allow isolated workspaces') && !button(document, 'Allow isolated workspaces').disabled, 'workspace permission control');
    button(document, 'Allow isolated workspaces').click();
    await until(async () => (await status()).environment_grant?.active === true, 'workspace permission readback');
    await click('Refresh roles');
    // Reuse the real placement command/CAS, not browser-chosen runtime IDs.
    const firstPlacement = (await api('GET', `${path}/placement`)).body.placement;
    assert((await api('PUT', `${path}/placement`, {state: 'active', expected_revision: firstPlacement.revision, runtime: 'f'.repeat(32)})).status === 400, 'browser chose another controller');
    assert((await api('PUT', `${path}/placement`, {state: 'retiring', expected_revision: firstPlacement.revision})).status === 200, 'competing placement transition');
    await until(() => button(document.querySelector('soda-desktop-placement'), 'Retire controller') && !button(document.querySelector('soda-desktop-placement'), 'Retire controller').disabled, 'stale controller action');
    button(document.querySelector('soda-desktop-placement'), 'Retire controller').click();
    await until(() => document.querySelector('soda-desktop-placement').textContent.includes('could not be confirmed'), 'stale placement refused');
    button(document.querySelector('soda-desktop-placement'), 'Refresh controller').click();
    await until(() => button(document.querySelector('soda-desktop-placement'), 'Finish retirement') && !button(document.querySelector('soda-desktop-placement'), 'Finish retirement').disabled, 'current retirement observation');
    button(document.querySelector('soda-desktop-placement'), 'Finish retirement').click();
    await until(() => button(document.querySelector('soda-desktop-placement'), 'Use this computer') && !button(document.querySelector('soda-desktop-placement'), 'Use this computer').disabled, 'inactive controller');
    button(document.querySelector('soda-desktop-placement'), 'Use this computer').click();
    await until(async () => {
      const next = (await api('GET', `${path}/placement`)).body.placement;
      return next.state === 'active' && BigInt(next.generation) === BigInt(firstPlacement.generation) + 1n;
    }, 'new placement generation');
    await until(() => button(document, 'Allow isolated workspaces') && !button(document, 'Allow isolated workspaces').disabled, 'stale workspace permission unavailable');
    await click('Refresh roles');
    assert(!offered(), 'stale workspace generation offered the login');
    button(document, 'Allow isolated workspaces').click();
    await until(async () => (await status()).environment_grant.native.generation !== firstPlacement.generation, 'explicit workspace reenrollment');
    await click('Refresh roles');
    assert(offered() && (await status()).sponsorships.length === 0, 'workspace permission authorized a login by itself');
    for (const role of ['Coding', 'Review']) {
      assert(roles.querySelector(`[aria-label="${role} login"]`).value === '', 'a role login was chosen automatically');
      input(roles, `${role} login`, sourceID); input(roles, `${role} model`, model);
    }
    await click('Save role selections');
    const funded = await status();
    assert(funded.sponsorships.length === 1 && funded.sponsorships[0].active && funded.sponsorships[0].roles.length === 2 && funded.policy.enabled === false, 'saving roles did not authorize exactly the chosen login, or enabled automation');
    await click('Refresh roles');
    const selected = await status();
    assert(selected.policy.enabled === false && !selected.policy.judgments && ['soda-coder', 'soda-reviewer'].every(role => selected.policy.roles[role].account === sourceID && selected.policy.roles[role].model === model), 'explicit independent choices lost');
    await click('Enroll automation policy');
    assert((await status()).policy.enabled === true, 'explicit policy enrollment failed');
    assert((await status()).spending_sources.find(source => source.connection === sourceID).execution_available === false && document.querySelector('#runtime-status').textContent.includes('execution is unavailable'), 'missing execution port became available');
    // A role removed from the login elsewhere is authorized again by the next role save.
    const saved = selected.sponsorships[0];
    assert((await api('PUT', `${path}/sponsorships/${sourceID}`, {expected_revision: saved.revision, roles: saved.roles.filter(role => role !== 'soda-coder'), active: true})).status === 200, 'competing login revision');
    await click('Refresh roles'); await click('Save role selections');
    const restored = await status();
    assert(restored.sponsorships[0].roles.length === 2 && restored.sponsorships[0].revision === saved.revision + 2 && restored.policy.roles['soda-coder'].account === sourceID, 'role save did not restore the chosen login for Coding');
    const current = await status(), p = current.policy;
    const actor = value => { const {kind, ...ref} = value; return ref; };
    const change = {expected_revision: p.revision, enabled: p.enabled, paused: p.paused, target_branch: p.target_branch, merge_method: p.merge_method,
      required_checks: ['fixture/check', 'fixture/concurrent'], roles: p.roles, judgments: null, attempt_limits: p.attempt_limits,
      reassessment_minutes: p.reassessment_minutes,
      publish_actor: actor(p.publish_actor), create_actor: actor(p.create_actor), review_actor: actor(p.review_actor), merge_actor: actor(p.merge_actor)};
    assert((await api('PUT', `${path}/policy`, change)).status === 200, 'competing policy revision');
    await click('Save role selections');
    assert(roles.textContent.includes('Policy changed'), 'stale policy was not refused');
    await click('Refresh roles'); await click('Save role selections');
    assert((await status()).policy.required_checks.includes('fixture/concurrent'), 'role save erased concurrent policy');
    await until(() => button(document, 'Withdraw workspace permission') && !button(document, 'Withdraw workspace permission').disabled, 'workspace withdrawal control');
    button(document, 'Withdraw workspace permission').click();
    await until(async () => (await status()).environment_grant?.active === false, 'workspace withdrawal readback');
    await click('Refresh roles');
    const authorized = (await status()).sponsorships[0];
    assert((await api('PUT', `${path}/sponsorships/${sourceID}`, {expected_revision: authorized.revision, roles: authorized.roles, active: false})).status === 200, 'withdrawal independent of workspace grant');
    await click('Refresh roles');
    const withdrawn = await status();
    assert(withdrawn.sponsorships[0].active === false && withdrawn.policy.enabled === true && !withdrawn.policy.judgments && ['soda-coder', 'soda-reviewer'].every(role => withdrawn.policy.roles[role].account === sourceID), 'withdrawal selected a fallback or rewrote enrollment');
    assert(roles.textContent.includes('Execution through this login is currently unavailable.'), 'withdrawn login still labeled available');
    await click('Disable automation policy');
    const disabled = await status();
    assert(disabled.policy.enabled === false && !disabled.policy.judgments, 'disable after funding withdrawal lost policy choices');
    const revision = disabled.policy.revision;
    assert(button(roles, 'Enroll automation policy').disabled, 'withdrawn funding permitted reenrollment');
    // Actual repository and account replacement retire the preceding transport.
    await until(() => [...repository.list.options].some(option => option.value === 'owned/other'), 'second observed repository');
    repository.value = 'owned/other'; document.querySelector('#repository-find').requestSubmit();
    await until(() => document.querySelector('#connection-status').textContent === 'Connected to owned/other.', 'repository replacement');
    let refused = false;
    try { await context.transport.request(`${path}/policy`, {method: 'PUT', body: JSON.stringify({...change, expected_revision: revision})}); } catch { refused = true; }
    assert(refused, 'previous repository retained mutation authority');
    account.value = __OTHER_CONNECTION__; account.dispatchEvent(new Event('change', {bubbles: true}));
    await until(() => !repository.disabled && [...repository.list.options].some(option => option.value === 'owned/repo'), 'second account catalogue');
    repository.value = 'owned/repo'; document.querySelector('#repository-find').requestSubmit();
    await until(() => document.querySelector('#connection-status').textContent === 'Connected to owned/repo.', 'account replacement');
    button(app.querySelector('nav[aria-label="Factory"]'), 'Work').click();
    await until(() => {
      const next = app.querySelector('soda-factory-role-settings')?.current;
      return next && next.actor !== context.actor;
    }, 'replacement role context');
    const replacement = app.querySelector('soda-factory-role-settings').current;
    const afterReplacement = await replacement.transport.request(`repositories/${replacement.repository}/factory`, {method: 'GET'});
    const retained = await afterReplacement.json();
    assert(retained.policy.revision === revision && retained.policy.enabled === false && retained.sponsorships[0].active === false, 'account/repository replacement mutated enrollment or revived funding');
    document.querySelector('#connection-disconnect').click();
    await until(() => app.textContent.includes('Choose an account to open Factory.'), 'view retirement');
    await invoke('report', {stage: 'owner-policy-complete', error: null});
  } catch (error) {
    await invoke('report', {stage: 'failed', error: String(error)});
  }
})();

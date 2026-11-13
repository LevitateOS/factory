(async () => {
  const invoke = window.__TAURI__.core.invoke;
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  let waiting = '';
  const until = async (check, why) => {
    waiting = why;
    const end = Date.now() + 45000;
    while (!await check()) {
      assert(Date.now() < end, 'Owner policy deadline: ' + why + '; ' + document.querySelector('#runtime-placement')?.textContent);
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
    const repository = document.querySelector('#factory-repository');
    await until(() => !repository.disabled && [...repository.options].some(o => o.value === '123'), 'observed repository');
    repository.value = '123'; repository.dispatchEvent(new Event('change', {bubbles: true}));
    await until(() => document.querySelector('#connection-status').textContent === 'Connected to owned/repo.', 'native product binding');
    const app = document.querySelector('soda-factory-app');
    button(app.querySelector('nav[aria-label="Factory"]'), 'Work').click();
    await until(() => app.querySelector('soda-factory-role-settings')?.querySelector('[aria-label="Coding model"]'), 'shared settings');
    const roles = app.querySelector('soda-factory-role-settings');
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
    assert(source && source.native.authentication === 'muse_login' && !source.native.credential_sha256 && source.judgment_models.length === 1, 'CLI-owned source');
    const sourceID = source.connection, model = source.judgment_models[0];
    assert(button(roles, 'Enroll automation policy') === undefined, 'initial policy enrolled automatically');
    input(roles, 'Coding model', model); input(roles, 'Review model', model);
    assert(roles.querySelector('[aria-label="Judgments model"]').value === '' && roles.querySelector('[aria-label="Judgments spending source"]').value === '', 'judgment was selected automatically');
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
    await until(() => button(document.querySelector('#runtime-placement'), 'Use this computer') && !button(document.querySelector('#runtime-placement'), 'Use this computer').disabled, 'controller observation settled');
    button(document.querySelector('#runtime-placement'), 'Use this computer').click();
    await until(async () => (await api('GET', `${path}/placement`)).body.placement?.state === 'active', 'controller readback');
    await until(() => button(document, 'Allow this repository to use this computer') && !button(document, 'Allow this repository to use this computer').disabled, 'current local consent');
    const permission = button(document, 'Allow this repository to use this computer');
    assert(permission && !permission.disabled, 'local repository consent'); permission.click();
    await until(async () => (await status()).operator_grant?.active === true, 'operator consent readback');
    input(roles, 'Spending source', sourceID); input(roles, 'Source allowance in minutes', '30');
    await until(() => button(roles, 'Save source allowance').disabled && roles.textContent.includes('workspace permission'), 'missing workspace grant unavailable');
    await until(() => button(document, 'Allow isolated workspaces') && !button(document, 'Allow isolated workspaces').disabled, 'workspace permission control');
    button(document, 'Allow isolated workspaces').click();
    await until(async () => (await status()).environment_grant?.active === true, 'workspace permission readback');
    await click('Refresh roles');
    // Reuse the real placement command/CAS, not browser-chosen runtime IDs.
    const firstPlacement = (await api('GET', `${path}/placement`)).body.placement;
    assert((await api('PUT', `${path}/placement`, {state: 'active', expected_revision: firstPlacement.revision, runtime: 'f'.repeat(32)})).status === 400, 'browser chose another controller');
    assert((await api('PUT', `${path}/placement`, {state: 'retiring', expected_revision: firstPlacement.revision})).status === 200, 'competing placement transition');
    await until(() => button(document.querySelector('#runtime-placement'), 'Retire controller') && !button(document.querySelector('#runtime-placement'), 'Retire controller').disabled, 'stale controller action');
    button(document.querySelector('#runtime-placement'), 'Retire controller').click();
    await until(() => document.querySelector('#runtime-placement').textContent.includes('could not be confirmed'), 'stale placement refused');
    button(document.querySelector('#runtime-placement'), 'Refresh controller').click();
    await until(() => button(document.querySelector('#runtime-placement'), 'Finish retirement') && !button(document.querySelector('#runtime-placement'), 'Finish retirement').disabled, 'current retirement observation');
    button(document.querySelector('#runtime-placement'), 'Finish retirement').click();
    await until(() => button(document.querySelector('#runtime-placement'), 'Use this computer') && !button(document.querySelector('#runtime-placement'), 'Use this computer').disabled, 'inactive controller');
    button(document.querySelector('#runtime-placement'), 'Use this computer').click();
    await until(async () => {
      const next = (await api('GET', `${path}/placement`)).body.placement;
      return next.state === 'active' && BigInt(next.generation) === BigInt(firstPlacement.generation) + 1n;
    }, 'new placement generation');
    await until(() => button(document, 'Allow isolated workspaces') && !button(document, 'Allow isolated workspaces').disabled, 'stale workspace permission unavailable');
    await click('Refresh roles');
    assert(button(roles, 'Save source allowance').disabled, 'stale workspace generation allowed funding');
    button(document, 'Allow isolated workspaces').click();
    await until(async () => (await status()).environment_grant.native.generation !== firstPlacement.generation, 'explicit workspace reenrollment');
    await click('Refresh roles');
    await click('Save source allowance');
    const funded = await status();
    assert(funded.sponsorships[0].active && funded.sponsorships[0].allowance_minutes === 30 && funded.policy.enabled === false, 'explicit allowance enabled automation');
    for (const role of ['Coding', 'Review', 'Judgments']) {
      assert(roles.querySelector(`[aria-label="${role} spending source"]`).value === '', 'funding automatically chose role source');
      input(roles, `${role} spending source`, sourceID); input(roles, `${role} model`, model);
    }
    await click('Save role selections');
    await until(async () => (await status()).spending_sources.some(source => source.connection === sourceID && source.judgment_available), 'verified judgment adapter');
    await click('Refresh roles');
    assert(roles.textContent.includes('This source can fund Feed'), 'supported funded judgment model unavailable');
    input(roles, 'Judgments model', 'unsupported-owner-fixture');
    await until(() => roles.textContent.includes('Judgment execution through this source is currently unavailable.'), 'unsupported model unavailable');
    assert(roles.querySelector('[aria-label="Coding model"]').value === model && roles.querySelector('[aria-label="Review model"]').value === model, 'judgment edit changed agent choices');
    input(roles, 'Judgments model', model);
    await click('Save role selections');
    const selected = await status();
    assert(selected.policy.enabled === false && selected.policy.judgments.account === sourceID && selected.policy.judgments.model === model && ['soda-coder', 'soda-reviewer'].every(role => selected.policy.roles[role].account === sourceID && selected.policy.roles[role].model === model), 'explicit independent choices lost');
    await click('Enroll automation policy');
    assert((await status()).policy.enabled === true, 'explicit policy enrollment failed');
    assert((await status()).spending_sources.find(source => source.connection === sourceID).execution_available === false && document.querySelector('#runtime-status').textContent.includes('execution is unavailable'), 'missing execution port became available');
    const saved = selected.sponsorships[0];
    const competing = await api('PUT', `${path}/sponsorships/${sourceID}`, {expected_revision: saved.revision, roles: saved.roles, allowance_minutes: 20, active: true});
    assert(competing.status === 200, 'competing source revision');
    input(roles, 'Spending source', sourceID); input(roles, 'Source allowance in minutes', '15');
    await click('Save source allowance');
    assert(roles.textContent.includes('could not be confirmed'), 'stale allowance was not refused');
    assert((await status()).sponsorships[0].allowance_minutes === 20, 'conflict overwrote current allowance');
    await click('Refresh roles');
    input(roles, 'Spending source', sourceID);
    await roles.updateComplete;
    assert(roles.querySelector('[aria-label="Source allowance in minutes"]').value === '20', 'editing a saved source reset its minute limit');
    const codingAllowance = roles.querySelector('input[name="allowed_roles"][value="soda-coder"]');
    assert(codingAllowance?.checked, 'saved Coding permission missing');
    codingAllowance.click();
    await click('Save source allowance');
    const limited = await status();
    assert(limited.sponsorships[0].active && limited.sponsorships[0].allowance_minutes === 20 &&
      !limited.sponsorships[0].roles.includes('soda-coder') && limited.sponsorships[0].roles.includes('soda-reviewer') &&
      limited.sponsorships[0].roles.includes('factory-judgment') && limited.policy.roles['soda-coder'].account === sourceID,
      'role withdrawal changed the shared limit, other roles or selected source');
    roles.querySelector('input[name="allowed_roles"][value="soda-coder"]').click();
    await click('Save source allowance');
    assert((await status()).sponsorships[0].roles.length === 3, 'explicit role permission restoration failed');
    for (const checkbox of roles.querySelectorAll('input[name="allowed_roles"]')) checkbox.click();
    await until(() => button(roles, 'Save source allowance').disabled && !button(roles, 'Withdraw source allowance').disabled, 'empty active role grant refused');
    input(roles, 'Spending source', sourceID);
    const current = await status(), p = current.policy;
    const actor = value => { const {kind, ...ref} = value; return ref; };
    const change = {expected_revision: p.revision, enabled: p.enabled, paused: p.paused, target_branch: p.target_branch, merge_method: p.merge_method,
      required_checks: ['fixture/check', 'fixture/concurrent'], roles: p.roles, judgments: p.judgments, attempt_limits: p.attempt_limits,
      reassessment_minutes: p.reassessment_minutes, intent_minutes: p.intent_minutes,
      publish_actor: actor(p.publish_actor), create_actor: actor(p.create_actor), review_actor: actor(p.review_actor), merge_actor: actor(p.merge_actor)};
    assert((await api('PUT', `${path}/policy`, change)).status === 200, 'competing policy revision');
    await click('Save role selections');
    assert(roles.textContent.includes('Policy changed'), 'stale policy was not refused');
    await click('Refresh roles'); await click('Save role selections');
    assert((await status()).policy.required_checks.includes('fixture/concurrent'), 'role save erased concurrent policy');
    roles.context = {...context, actor: undefined};
    await until(() => idle() && roles.querySelector('[aria-label="Spending source"]'), 'unknown actor observation');
    input(roles, 'Spending source', sourceID);
    await until(() => button(roles, 'Withdraw source allowance').disabled, 'unknown actor withdrawal unavailable');
    roles.context = context;
    await until(() => idle() && roles.querySelector('[aria-label="Spending source"]'), 'current actor restored');
    await until(() => button(document, 'Withdraw workspace permission') && !button(document, 'Withdraw workspace permission').disabled, 'workspace withdrawal control');
    button(document, 'Withdraw workspace permission').click();
    await until(async () => (await status()).environment_grant?.active === false, 'workspace withdrawal readback');
    await click('Refresh roles');
    input(roles, 'Spending source', sourceID);
    await until(() => button(roles, 'Save source allowance').disabled && !button(roles, 'Withdraw source allowance').disabled, 'withdrawal independent of workspace grant');
    await click('Withdraw source allowance');
    const withdrawn = await status();
    assert(withdrawn.sponsorships[0].active === false && withdrawn.policy.enabled === true && withdrawn.policy.judgments.account === sourceID && ['soda-coder', 'soda-reviewer'].every(role => withdrawn.policy.roles[role].account === sourceID), 'withdrawal selected a fallback or rewrote enrollment');
    assert(roles.textContent.includes('Judgment execution through this source is currently unavailable.'), 'withdrawn allowance still labeled available');
    await click('Disable automation policy');
    const disabled = await status();
    assert(disabled.policy.enabled === false && disabled.policy.judgments.account === sourceID, 'disable after funding withdrawal lost policy choices');
    const revision = disabled.policy.revision;
    assert(button(roles, 'Enroll automation policy').disabled, 'withdrawn funding permitted reenrollment');
    // Actual repository and account replacement retire the preceding transport.
    await until(() => [...repository.options].some(option => option.value === '124'), 'second observed repository');
    repository.value = '124'; repository.dispatchEvent(new Event('change', {bubbles: true}));
    await until(() => document.querySelector('#connection-status').textContent === 'Connected to owned/other.', 'repository replacement');
    let refused = false;
    try { await context.transport.request(`${path}/policy`, {method: 'PUT', body: JSON.stringify({...change, expected_revision: revision})}); } catch { refused = true; }
    assert(refused, 'previous repository retained mutation authority');
    account.value = __OTHER_CONNECTION__; account.dispatchEvent(new Event('change', {bubbles: true}));
    await until(() => !repository.disabled && [...repository.options].some(option => option.value === '123'), 'second account catalogue');
    repository.value = '123'; repository.dispatchEvent(new Event('change', {bubbles: true}));
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

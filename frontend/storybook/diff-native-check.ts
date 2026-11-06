// Actual WebKitGTK workbench integration. External compositor input supplies
// trusted keyboard events; calling the report command cannot grant app powers.
import {diffFiles} from './diff-fixtures.js';
import requiredChecks from './diff-checks.json';
import type {SodaFactoryDiff} from '../factory/review/diff.js';

export async function runNativeDiffCheck() {
  const checks: string[] = [];
  const native = (
    window as unknown as {__TAURI__: {core: {invoke: (command: string, args: unknown) => Promise<unknown>}}}
  ).__TAURI__.core;
  const wait = async (predicate: () => unknown, label = 'condition') => {
    const deadline = performance.now() + 30000;
    while (!predicate()) {
      if (performance.now() > deadline) throw Error('Timed out at ' + label + ': ' + checks.join(', '));
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  };
  const require = (value: unknown, name: string) => {
    if (!requiredChecks.includes(name)) throw Error('Unknown native diff check: ' + name);
    if (!value) throw Error(name);
    checks.push(name);
  };
  try {
    await wait(() => document.querySelector('soda-factory-diff'));
    const component = document.querySelector<SodaFactoryDiff>('soda-factory-diff');
    if (!component) throw Error('Missing component');
    const root = component.shadowRoot;
    if (!root) throw Error('Missing render root');
    require(root.textContent?.includes('Original diff context is unavailable.') &&
      root.textContent.includes('Retained conversation with unknown original context.'), 'unknown-thread-retained');
    const code = () => root.querySelector('diffs-container')?.shadowRoot;
    await wait(() => code()?.querySelector('[data-line]'));
    require(code()?.textContent?.includes('before'), 'render');
    let trustedEnter = false,
      trustedText = false;
    root.addEventListener('keydown', (event) => {
      if (event.isTrusted && event instanceof KeyboardEvent && event.key === 'Enter') trustedEnter = true;
    });
    root.addEventListener('input', (event) => {
      if (event.isTrusted && event.target instanceof HTMLTextAreaElement) trustedText = true;
    });
    root.querySelector<HTMLInputElement>('[name=start]')?.focus();
    let selected = false;
    component.addEventListener('factory-review-selection', () => {
      selected = true;
    });
    await native.invoke('diff_stage', {stage: 'selection'});
    await wait(() => selected);
    require(trustedEnter, 'trusted-keyboard');
    const button = (text: string) => [...root.querySelectorAll('button')].find((el) => el.textContent?.trim() === text);
    button('Comment on selection')?.click();
    await wait(() => component.draft);
    require(component.draft?.anchor.side === 'old' &&
      component.draft.anchor.lines?.start === 2 &&
      component.draft.anchor.lines.end === 3, 'old-anchor');
    const editor = root.querySelector<HTMLTextAreaElement>('textarea');
    editor?.focus();
    await native.invoke('diff_stage', {stage: 'draft'});
    await wait(() => component.draft?.body === 'Native review café');
    require(trustedText, 'trusted-draft');
    const original = JSON.stringify(component.draft);
    root.querySelector<HTMLButtonElement>('[aria-label="Toggle diff layout"]')?.click();
    await component.updateComplete;
    await wait(() => code()?.querySelector('[data-diff-type="unified"]') || code()?.querySelector('[data-line]'));
    require(JSON.stringify(component.draft) === original, 'layout');
    component.file = diffFiles.long;
    await component.updateComplete;
    await wait(() => code()?.querySelector('[data-line]'));
    const viewport = root.querySelector<HTMLElement>('.viewport');
    if (!viewport) throw Error('Missing scroll viewport');
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
    viewport.scrollTop = viewport.scrollHeight;
    await wait(() => code()?.textContent?.includes('next6000'), 'final virtualized line');
    require((code()?.querySelectorAll('[data-line]').length ?? 6000) < 1000 &&
      JSON.stringify(component.draft) === original, 'virtualization');
    component.file = {...diffFiles.renamed, diff: {...diffFiles.renamed.diff, head: 'd'.repeat(40)}};
    await component.updateComplete;
    require(JSON.stringify(component.draft) === original &&
      root.textContent?.includes('original revision and path'), 'revision-pin');
    for (const state of ['binary', 'truncated', 'unavailable'] as const) {
      component.file = diffFiles[state];
      await component.updateComplete;
      if (root.querySelector('diffs-container')) throw Error('Rendered missing patch');
    }
    checks.push('states');
    for (const state of ['added', 'deleted'] as const) {
      component.file = diffFiles[state];
      await component.updateComplete;
      await wait(() => code()?.querySelector('[data-line]'));
    }
    checks.push('added-deleted');
    component.file = diffFiles.renamed;
    await component.updateComplete;
    await wait(() => code()?.querySelector('[data-line]'));
    button('Discard draft')?.click();
    await wait(() => !component.draft);
    const side = root.querySelector<HTMLSelectElement>('[name=side]');
    if (side) side.value = 'new';
    const first = root.querySelector<HTMLInputElement>('[name=start]');
    const last = root.querySelector<HTMLInputElement>('[name=end]');
    if (first) first.value = '2';
    if (last) last.value = '3';
    root.querySelector<HTMLFormElement>('form')?.requestSubmit();
    await component.updateComplete;
    button('Comment on selection')?.click();
    await wait(() => component.draft);
    require(component.draft?.anchor.side === 'new' &&
      component.draft.anchor.lines?.start === 2 &&
      component.draft.anchor.lines.end === 3, 'new-anchor');
    const renderedThread = (root.textContent ?? '') + (code()?.textContent ?? '');
    require(root.querySelector('.factory-inline-thread img') === null &&
      code()?.querySelector('.factory-inline-thread img') === null &&
      renderedThread.includes('retain') && !renderedThread.includes('<img src=x onerror=alert(1)>'), 'safe-thread');
    component.file = {...diffFiles.renamed, patch: 'invalid patch'};
    await component.updateComplete;
    await wait(() => root.textContent?.includes('could not be rendered safely'), 'invalid patch notice');
    require(root.textContent?.includes('could not be rendered safely'), 'invalid-patch');
    component.file = diffFiles.renamed;
    await component.updateComplete;
    await wait(() => code()?.querySelector('[data-line]'));
    component.remove();
    require(component.shadowRoot?.querySelector('diffs-container') === null, 'disposal');
    if (!requiredChecks.every((name) => checks.includes(name))) throw Error('Incomplete native diff checks');
    await native.invoke('report_diff', {checks, error: null});
  } catch (error) {
    await native.invoke('report_diff', {
      checks,
      error: error instanceof Error ? error.message : 'Native diff check failed',
    });
  }
}

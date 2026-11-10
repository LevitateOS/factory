import type {Core} from './transport.js';

type Context = {mode: string; session: string; window: string; generation: number; binding: unknown};
type Target = {within?: string; role?: string; name?: string; node?: string};
type Request = {
  op: string;
  scope?: string;
  action?: string;
  target?: Target;
  value?: string;
  expected?: string | boolean;
  checked?: boolean;
  state?: string;
  timeout_ms?: number;
};
type Envelope = {nonce: string; request: Request; deadline_unix_ms: number};
type Result = {ok: boolean; error?: string; [key: string]: unknown};
type Handle = {element: HTMLElement; fingerprint: string};
const failed = (error: string, extra: Record<string, unknown> = {}): Result => ({ok: false, error, ...extra});
const clip = (s: string) => s.slice(0, 4096);
const normalize = (s: string) => s.replace(/\s+/g, ' ').trim();

/** Fixed operations over rendered controls. No arbitrary script or product write port. */
export async function mountDeveloper(core: Core) {
  const initial = await core.invoke<Context>('developer_context');
  if (initial.mode === 'disabled') return;
  const documentID = crypto.randomUUID();
  const waits = new Map<string, AbortController>();
  const snapshots: {created: number; scope: string; handles: Map<string, Handle>}[] = [];
  let stopped = false;
  const indicator = document.createElement('p');
  indicator.id = 'factory-developer-mode';
  indicator.setAttribute('role', 'status');
  indicator.textContent = `Developer tools: ${initial.mode}`;
  document.body.prepend(indicator);

  const elements = (): HTMLElement[] => {
    const all: HTMLElement[] = [];
    const visit = (root: Document | ShadowRoot) => {
      for (const element of root.querySelectorAll<HTMLElement>('*')) {
        all.push(element);
        if (element.shadowRoot) visit(element.shadowRoot);
      }
    };
    visit(document);
    return all;
  };
  const visible = (element: HTMLElement): boolean => {
    if (!element.isConnected || !element.getClientRects().length) return false;
    for (
      let current: HTMLElement | null = element;
      current;
      current =
        current.parentElement ??
        (current.getRootNode() instanceof ShadowRoot
          ? ((current.getRootNode() as ShadowRoot).host as HTMLElement)
          : null)
    ) {
      if (current.hidden || current.inert || current.getAttribute('aria-hidden') === 'true') return false;
      const style = getComputedStyle(current);
      if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) === 0) return false;
    }
    return true;
  };
  const role = (element: HTMLElement): string => {
    const explicit = element.getAttribute('role');
    if (explicit) return explicit.split(' ')[0]!;
    switch (element.tagName) {
      case 'BUTTON':
      case 'SUMMARY':
        return 'button';
      case 'A':
        return element.hasAttribute('href') ? 'link' : '';
      case 'TEXTAREA':
        return 'textbox';
      case 'SELECT':
        return (element as HTMLSelectElement).multiple ? 'listbox' : 'combobox';
      case 'INPUT': {
        const type = (element as HTMLInputElement).type;
        return type === 'checkbox' || type === 'radio'
          ? type
          : ['button', 'submit', 'reset'].includes(type)
            ? 'button'
            : ['text', 'search', 'email', 'url', 'tel', 'number', 'password'].includes(type)
              ? 'textbox'
              : '';
      }
      case 'NAV':
        return 'navigation';
      case 'FORM':
        return 'form';
      case 'SECTION':
        return 'region';
      case 'DIALOG':
        return 'dialog';
      case 'H1':
      case 'H2':
      case 'H3':
      case 'H4':
        return 'heading';
      default:
        return '';
    }
  };
  const secret = (element: HTMLElement) =>
    element instanceof HTMLInputElement && ['password', 'file', 'hidden'].includes(element.type);
  const name = (element: HTMLElement): string => {
    const by = element.getAttribute('aria-labelledby');
    if (by) {
      const root = element.getRootNode() as Document | ShadowRoot;
      return normalize(
        by
          .split(/\s+/)
          .map((id) => root.querySelector(`#${CSS.escape(id)}`)?.textContent ?? '')
          .join(' ')
      );
    }
    const aria = element.getAttribute('aria-label');
    if (aria) return normalize(aria);
    if (
      element instanceof HTMLInputElement ||
      element instanceof HTMLSelectElement ||
      element instanceof HTMLTextAreaElement
    ) {
      const label = [...(element.labels ?? [])]
        .map((label) => {
          // A label's select options are values, not part of its control name.
          const copy = label.cloneNode(true) as HTMLElement;
          copy.querySelectorAll('input,textarea,select,button').forEach((node) => node.remove());
          return copy.textContent ?? '';
        })
        .join(' ');
      if (label) return normalize(label);
      return element instanceof HTMLInputElement && ['submit', 'button', 'reset'].includes(element.type)
        ? element.value
        : '';
    }
    return ['button', 'link', 'heading', 'alert', 'status'].includes(role(element))
      ? normalize(element.textContent ?? '')
      : '';
  };
  const disabled = (element: HTMLElement) =>
    element.matches(':disabled') || element.getAttribute('aria-disabled') === 'true' || !!element.closest('[inert]');
  const value = (element: HTMLElement) =>
    !secret(element) &&
    (element instanceof HTMLInputElement ||
      element instanceof HTMLTextAreaElement ||
      element instanceof HTMLSelectElement)
      ? element.value
      : undefined;
  const fingerprint = (element: HTMLElement) =>
    JSON.stringify([
      role(element),
      name(element),
      element.getAttribute('type'),
      disabled(element),
      value(element),
      element instanceof HTMLInputElement ? element.checked : null,
    ]);
  const scope = (context: Context) =>
    JSON.stringify({
      session: context.session,
      window: context.window,
      document: documentID,
      generation: context.generation,
      binding: context.binding,
      account: document.querySelector<HTMLSelectElement>('#factory-account')?.value ?? '',
      repository: document.querySelector<HTMLSelectElement>('#factory-repository')?.value ?? '',
    });
  const describe = (element: HTMLElement) => {
    const rect = element.getBoundingClientRect();
    return {
      role: role(element),
      name: clip(name(element)),
      text: ['status', 'alert', 'heading'].includes(role(element))
        ? clip(normalize(element.textContent ?? ''))
        : undefined,
      disabled: disabled(element),
      readonly: element.matches('[readonly]'),
      value: value(element) === undefined ? undefined : clip(value(element)!),
      value_truncated: (value(element)?.length ?? 0) > 4096,
      redacted: secret(element),
      checked:
        element instanceof HTMLInputElement && ['checkbox', 'radio'].includes(element.type)
          ? element.checked
          : undefined,
      focused: element === document.activeElement,
      offscreen: rect.bottom <= 0 || rect.top >= innerHeight || rect.right <= 0 || rect.left >= innerWidth,
      type: element.getAttribute('type') ?? element.tagName.toLowerCase(),
      options_truncated: element instanceof HTMLSelectElement && element.options.length > 100,
      options:
        element instanceof HTMLSelectElement
          ? [...element.options].slice(0, 100).map((option) => ({
              value: clip(option.value),
              name: clip(option.textContent ?? ''),
              disabled: option.disabled,
              selected: option.selected,
            }))
          : undefined,
    };
  };
  const tree = (context: Context): Result => {
    const handles = new Map<string, Handle>(),
      nodes: Record<string, unknown>[] = [];
    let bytes = 0,
      truncated = false;
    for (const element of elements()) {
      if (!visible(element) || !role(element)) continue;
      const description = describe(element),
        id = crypto.randomUUID();
      bytes += new TextEncoder().encode(JSON.stringify(description)).length;
      if (nodes.length === 500 || bytes > 700000) {
        truncated = true;
        break;
      }
      handles.set(id, {element, fingerprint: fingerprint(element)});
      nodes.push({node: id, ...description});
    }
    snapshots.push({created: Date.now(), scope: scope(context), handles});
    while (snapshots.length > 2) snapshots.shift();
    return {ok: true, scope: scope(context), destination: location.hash, nodes, truncated};
  };
  const locate = (target: Target, currentScope: string): HTMLElement | Result => {
    if (target.node) {
      const entry = snapshots
        .filter((s) => s.scope === currentScope && Date.now() - s.created < 60000)
        .flatMap((s) => [...s.handles.entries()])
        .find(([id]) => id === target.node)?.[1];
      if (!entry || !visible(entry.element) || fingerprint(entry.element) !== entry.fingerprint)
        return failed('stale_target');
      return entry.element;
    }
    let candidates = elements().filter(
      (element) => visible(element) && (!target.role || role(element) === target.role) && name(element) === target.name
    );
    if (target.within) {
      const regions = elements().filter(
        (element) =>
          visible(element) &&
          ['region', 'navigation', 'form', 'dialog'].includes(role(element)) &&
          name(element) === target.within
      );
      if (regions.length !== 1) return failed(regions.length ? 'ambiguous_region' : 'not_found');
      candidates = candidates.filter(
        (element) =>
          regions[0]!.contains(element) ||
          (element.getRootNode() instanceof ShadowRoot &&
            regions[0]!.contains((element.getRootNode() as ShadowRoot).host))
      );
    }
    if (candidates.length !== 1)
      return failed(candidates.length ? 'ambiguous_target' : 'not_found', {
        candidates: candidates.slice(0, 20).map(describe),
      });
    return candidates[0]!;
  };
  const perform = (request: Request, context: Context): Result => {
    if (request.op === 'tree') return tree(context);
    const found = locate(request.target!, scope(context));
    if (!(found instanceof HTMLElement)) return found;
    if (disabled(found)) return failed('disabled');
    if (found.matches('[readonly]') || secret(found)) return failed('unsupported_control');
    found.scrollIntoView({block: 'center', inline: 'nearest', behavior: 'instant'});
    if (request.op === 'click') {
      if (!['button', 'link', 'checkbox', 'radio'].includes(role(found))) return failed('unsupported_control');
      found.click();
    } else if (request.op === 'check') {
      if (!(found instanceof HTMLInputElement) || found.type !== 'checkbox') return failed('unsupported_control');
      if (found.checked !== request.expected) return failed('changed_value');
      if (found.checked !== request.checked) found.click();
    } else if (request.op === 'fill' || request.op === 'select') {
      const isText =
        found instanceof HTMLTextAreaElement ||
        (found instanceof HTMLInputElement && ['text', 'search', 'email', 'url', 'tel', 'number'].includes(found.type));
      if (request.op === 'fill' ? !isText : !(found instanceof HTMLSelectElement)) return failed('unsupported_control');
      if (value(found) !== request.expected) return failed('changed_value');
      if (
        found instanceof HTMLSelectElement &&
        ![...found.options].some(
          (option) =>
            option.value === request.value &&
            !option.disabled &&
            !(option.parentElement instanceof HTMLOptGroupElement && option.parentElement.disabled)
        )
      )
        return failed('invalid_option');
      const proto =
        found instanceof HTMLInputElement
          ? HTMLInputElement.prototype
          : found instanceof HTMLTextAreaElement
            ? HTMLTextAreaElement.prototype
            : HTMLSelectElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(found, request.value);
      found.dispatchEvent(new Event('input', {bubbles: true}));
      if (request.op === 'select') found.dispatchEvent(new Event('change', {bubbles: true}));
    } else return failed('unsupported_operation');
    return {ok: true, state: 'dispatched', synthetic: true};
  };
  const run = async (envelope: Envelope, signal: AbortSignal): Promise<Result> => {
    const {request} = envelope;
    const context = await core.invoke<Context>('developer_context');
    if (signal.aborted || stopped || context.mode === 'disabled') return failed('retired');
    if (Date.now() > envelope.deadline_unix_ms) return failed('deadline');
    if (request.scope && request.scope !== scope(context)) return failed('stale_scope');
    if (request.op !== 'tree' && request.op !== 'wait' && context.mode !== 'control')
      return failed('control_mode_required');
    if (request.op !== 'wait') return perform(request, context);
    return new Promise<Result>((resolve) => {
      let reading = false,
        done = false;
      const finish = (result: Result) => {
        if (done) return;
        done = true;
        observer.disconnect();
        clearInterval(timer);
        clearTimeout(deadline);
        signal.removeEventListener('abort', abort);
        document.removeEventListener('input', check);
        document.removeEventListener('change', check);
        resolve(result);
      };
      const check = () => {
        void evaluate();
      };
      const abort = () => finish(failed('retired'));
      const evaluate = async () => {
        if (reading || done) return;
        reading = true;
        try {
          const current = await core.invoke<Context>('developer_context');
          if (done) return;
          if (current.mode === 'disabled' || request.scope !== scope(current)) {
            finish(failed('stale_scope'));
            return;
          }
          const found = locate(request.target!, scope(current));
          if (!(found instanceof HTMLElement)) {
            if (found.error === 'not_found' && request.state === 'absent') finish({ok: true, state: 'matched'});
            else if (found.error !== 'not_found') finish(found);
          } else {
            const checked = found instanceof HTMLInputElement && found.checked;
            if (
              request.state === 'present' ||
              (request.state === 'enabled' && !disabled(found)) ||
              (request.state === 'disabled' && disabled(found)) ||
              (request.state === 'checked' && checked) ||
              (request.state === 'unchecked' && found instanceof HTMLInputElement && !checked)
            )
              finish({ok: true, state: 'matched', target: describe(found)});
          }
        } catch {
          finish(failed('retired'));
        } finally {
          reading = false;
        }
      };
      const observer = new MutationObserver(check);
      observer.observe(document.body, {subtree: true, attributes: true, childList: true, characterData: true});
      for (const element of elements())
        if (element.shadowRoot)
          observer.observe(element.shadowRoot, {subtree: true, attributes: true, childList: true, characterData: true});
      const timer = setInterval(check, 250);
      const deadline = setTimeout(
        () => finish(failed('deadline', {target: request.target})),
        Math.min(request.timeout_ms!, envelope.deadline_unix_ms - Date.now())
      );
      signal.addEventListener('abort', abort, {once: true});
      document.addEventListener('input', check);
      document.addEventListener('change', check);
      check();
    });
  };
  const driver = {
    dispatch(envelope: Envelope) {
      if (stopped || waits.has(envelope.nonce)) return;
      const controller = new AbortController();
      waits.set(envelope.nonce, controller);
      void run(envelope, controller.signal)
        .catch(() => failed('driver_error'))
        .then((result) => core.invoke('developer_reply', {nonce: envelope.nonce, result}))
        .catch(() => {})
        .finally(() => waits.delete(envelope.nonce));
    },
    cancel(nonce: string) {
      waits.get(nonce)?.abort();
    },
    stop() {
      stopped = true;
      for (const controller of waits.values()) controller.abort();
      waits.clear();
      snapshots.length = 0;
      indicator.remove();
    },
  };
  Object.defineProperty(window, '__factoryDeveloper', {value: driver, configurable: false, writable: false});
}

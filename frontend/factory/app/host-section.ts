import {html} from 'lit';
import {check} from '../api/models.js';

/** Host-only presentation contributed to a shared page, such as a desktop
 * control the shared UI cannot import. The page places it under its title and
 * owns when it mounts and retires; the element's requests and state stay with
 * the host. Hosts without such a control pass no sections. */
export interface FactoryHostSection {
  /** Page-unique key, also the section's in-page anchor. */
  readonly id: string;
  /** Plain-text heading and region name. */
  readonly title: string;
  /** Called once per mounted page; `signal` aborts when the page retires it. */
  mount(signal: AbortSignal): HTMLElement;
}

export interface MountedHostSection {
  readonly id: string;
  readonly title: string;
  readonly element: HTMLElement;
}

export function admitHostSections(value: unknown): void {
  check(value === undefined || (Array.isArray(value) && value.length <= 8));
  const sections = (value ?? []) as readonly FactoryHostSection[];
  check(new Set(sections.map((section) => section?.id)).size === sections.length);
  for (const section of sections)
    check(
      typeof section.id === 'string' &&
        /^[a-z][a-z0-9-]{0,31}$/.test(section.id) &&
        typeof section.title === 'string' &&
        !!section.title.trim() &&
        section.title.length <= 80 &&
        !/[\u0000-\u001f\u007f]/.test(section.title) &&
        typeof section.mount === 'function'
    );
}

/** Mounts admitted sections for one page lifetime. */
export function mountHostSections(
  sections: readonly FactoryHostSection[] | undefined,
  signal: AbortSignal
): MountedHostSection[] {
  return (sections ?? []).map(({id, title, mount}) => {
    const element = mount(signal);
    check(element instanceof HTMLElement);
    signal.addEventListener('abort', () => element.remove(), {once: true});
    return {id, title, element};
  });
}

export function hostSectionAnchor(prefix: string, id: string) {
  return `${prefix}-${id}`;
}

export function renderHostSection(prefix: string, section: MountedHostSection) {
  const anchor = hostSectionAnchor(prefix, section.id);
  return html`<section id=${anchor} class="soda-factory-host-section" aria-labelledby=${anchor + '-title'}>
    <h3 id=${anchor + '-title'}>${section.title}</h3>
    ${section.element}
  </section>`;
}

import {Marked} from 'marked';
import DOMPurify from 'dompurify';

const escape = (value: string) =>
  value.replace(/[&<>"']/g, (char) => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'})[char]!);
// Keep attachments explicit until a host supplies authenticated media reads.
const parser = new Marked({
  gfm: true,
  async: false,
  renderer: {
    image({href, text}) {
      return `<a href="${escape(href)}">${escape(text || 'Image attachment')}</a>`;
    },
  },
});

export function markdownLink(value: string): string | undefined {
  try {
    const url = new URL(value);
    if (!['https:', 'http:', 'mailto:'].includes(url.protocol) || url.username || url.password) return;
    return url.href;
  } catch {
    return;
  }
}

/** Sanitized DOM nodes, never provider HTML inserted directly into a template.
 * The host resolves provider-specific relative references before URL policy. */
export function issueMarkdown(
  body: string,
  resolveLink: (href: string) => string | undefined,
  section = 'issue'
): DocumentFragment {
  const fragment = DOMPurify.sanitize(parser.parse(body, {async: false}), {
    RETURN_DOM_FRAGMENT: true,
    ALLOWED_TAGS: [
      'p',
      'br',
      'hr',
      'h1',
      'h2',
      'h3',
      'h4',
      'h5',
      'h6',
      'strong',
      'em',
      'del',
      'blockquote',
      'pre',
      'code',
      'ul',
      'ol',
      'li',
      'a',
      'table',
      'thead',
      'tbody',
      'tr',
      'th',
      'td',
      'input',
      'details',
      'summary',
      'kbd',
    ],
    ALLOWED_ATTR: ['href', 'title', 'start', 'type', 'checked', 'disabled', 'open', 'colspan', 'rowspan'],
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false,
  });
  // Heading targets belong to this rendered body. Scope comment headings so
  // identical text in a description and several comments cannot steal a jump.
  const headings = new Map<string, string>();
  const used = new Set<string>();
  const namespace = [...new TextEncoder().encode(section)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
  for (const heading of fragment.querySelectorAll('h1,h2,h3,h4,h5,h6')) {
    const base =
      (heading.textContent ?? '')
        .trim()
        .toLowerCase()
        .replace(/[^\p{L}\p{N}_\s-]/gu, '')
        .replace(/\s/g, '-') || 'section';
    let slug = base,
      duplicate = 0;
    while (used.has(slug)) slug = base + '-' + ++duplicate;
    used.add(slug);
    const target = 'user-content-' + namespace + '-' + slug;
    heading.id = target;
    headings.set(slug, target);
  }
  for (const anchor of fragment.querySelectorAll('a')) {
    let href = anchor.getAttribute('href');
    let safe: string | undefined;
    try {
      if (href?.startsWith('#')) {
        const heading = headings.get(decodeURIComponent(href.slice(1)));
        if (heading) href = '#' + encodeURIComponent(heading);
      }
      const resolved = href ? resolveLink(href) : undefined;
      safe = resolved ? markdownLink(resolved) : undefined;
    } catch {
      /* Unsupported host link remains text. */
    }
    if (!safe) anchor.replaceWith(...anchor.childNodes);
    else {
      anchor.href = safe;
      anchor.rel = 'noopener noreferrer';
    }
  }
  for (const input of fragment.querySelectorAll('input')) {
    input.type = 'checkbox';
    input.disabled = true;
  }
  return fragment;
}

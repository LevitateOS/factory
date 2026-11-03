import {html, svg} from 'lit';
import type {FeedGraphItem} from './feed-response.js';

// Edges come only from recorded IDs. Missing references remain visible; a
// cycle is displayed as a cycle rather than assigned a fictitious work order.
export function feedGraphLayout(items: readonly FeedGraphItem[]) {
  const byID = new Map(items.flatMap((item, index) => (item.id ? [[item.id, index] as const] : [])));
  const tiers = new Map<number, number>();
  const cyclic = new Set<number>();
  function tier(index: number, path = new Set<number>()): number {
    if (path.has(index)) {
      let inside = false;
      for (const member of path) {
        if (member === index) inside = true;
        if (inside) cyclic.add(member);
      }
      return 0;
    }
    const known = tiers.get(index);
    if (known !== undefined) return known;
    const next = new Set(path).add(index);
    const dependencies = (items[index]?.dependsOn ?? []).flatMap((id) => {
      const target = byID.get(id);
      return target === undefined ? [] : [target];
    });
    const level = dependencies.length ? 1 + Math.max(...dependencies.map((dep) => tier(dep, next))) : 0;
    tiers.set(index, cyclic.has(index) ? 0 : level);
    return tiers.get(index) ?? 0;
  }
  items.forEach((_, index) => tier(index));
  const occupied = new Map<number, number>();
  const nodes = items.map((item, index) => {
    const column = tiers.get(index) ?? 0,
      row = occupied.get(column) ?? 0;
    occupied.set(column, row + 1);
    return {item, index, x: column * 236 + 8, y: row * 108 + 8, cyclic: cyclic.has(index)};
  });
  const edges = nodes.flatMap((node) =>
    (node.item.dependsOn ?? []).flatMap((id) => {
      const from = nodes[byID.get(id) ?? -1];
      return from ? [{from, to: node}] : [];
    })
  );
  return {
    nodes,
    edges,
    width: Math.max(220, ...nodes.map((node) => node.x + 208)),
    height: Math.max(100, ...nodes.map((node) => node.y + 100)),
    cyclic: cyclic.size > 0,
  };
}

export function renderFeedGraph(items: readonly FeedGraphItem[], label = 'Work dependencies') {
  const layout = feedGraphLayout(items);
  return html`<section class="soda-feed-graph" aria-label=${label}>
    <div class="soda-feed-graph-scroll" tabindex="0" aria-label="Dependency graph; scroll to explore">
      <div class="soda-feed-graph-canvas" style=${`width:${layout.width}px;height:${layout.height}px`}>
        <svg width=${layout.width} height=${layout.height} aria-hidden="true" class="soda-feed-graph-lines">
          ${layout.edges.map(({from, to}) => {
            const x = from.x + 200,
              y = from.y + 43,
              endX = to.x,
              endY = to.y + 43;
            return svg`<path d=${`M${x},${y} C${x + 24},${y} ${endX - 24},${endY} ${endX},${endY}`}></path><path d=${`M${endX - 5},${endY - 4} L${endX},${endY} L${endX - 5},${endY + 4}`}></path>`;
          })}
        </svg>
        <ol>
          ${layout.nodes.map(
            ({item, x, y, cyclic}) => html`<li
              class="soda-feed-graph-node"
              style=${`left:${x}px;top:${y}px`}
              data-cycle=${cyclic ? 'true' : 'false'}
            >
              <strong>${item.label}</strong>
              ${item.state ? html`<span>${item.state}</span>` : item.detail ? html`<span>${item.detail}</span>` : ''}
              <span class="soda-feed-graph-dependencies"
                >${item.dependsOn?.length ? `After: ${item.dependsOn.join(', ')}` : item.dependsOn ? 'Starts independently' : 'Dependencies not supplied'}</span
              >
            </li>`
          )}
        </ol>
      </div>
    </div>
    ${layout.cyclic ? html`<p class="soda-feed-block-note" role="status">Circular dependencies recorded. Resolve them before treating this as a runnable order.</p>` : ''}
    <details>
      <summary>Task details · ${items.length}</summary>
      <dl>
        ${items.map(
          (item) =>
            html`<div>
              <dt>${item.id ? `${item.id} · ` : ''}${item.label}</dt>
              <dd>
                ${item.detail ?? item.state ?? 'No further detail supplied'}${item.dependsOn?.length ? html`<p>Depends on ${item.dependsOn.join(', ')}</p>` : ''}
              </dd>
            </div>`
        )}
      </dl>
    </details>
  </section>`;
}

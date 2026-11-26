import type {FactoryTransport} from '../factory/api/transport.js';
import type {WorkOverviewContext} from '../factory/work/progress.js';
import {progressStoryPeer} from './progress-fixtures.js';
import {watchRenderer, watchRun, watchStoryPeer} from './watch-fixtures.js';

/** The Work page over one transport: the run list from a fixture, the selected
 * run's output from the declared Watch peer, the plan from the progress fixture. */
export function workPageStoryPeer(runs: 'some' | 'none') {
  const progress = progressStoryPeer('ready'),
    watch = watchStoryPeer('live', progress.overview.repository);
  const listed =
    runs === 'some'
      ? [
          {id: watchRun, role: 'soda-coder', issue: '12', started: '2026-11-26T09:14:00Z', reconciled: false},
          {id: 'b'.repeat(32), role: 'soda-reviewer', issue: '11', started: '2026-11-25T16:02:00Z', outcome: 'succeeded', reconciled: true},
          {id: 'c'.repeat(32), role: 'soda-coder', issue: '11', started: '2026-11-25T15:20:00Z', outcome: 'failed', reconciled: true},
        ]
      : [];
  const transport: FactoryTransport = {
    get generation() {
      return watch.context.transport.generation;
    },
    assetBase: '/',
    request(path, init) {
      if (path.endsWith('/factory/runs')) return Promise.resolve(Response.json({runs: listed}));
      if (path.startsWith('factory/runs/')) return watch.context.transport.request(path, init);
      return progress.overview.transport.request(path, init);
    },
  };
  const {intentID: _intent, ...overview} = progress.overview;
  const context: WorkOverviewContext = {
    ...overview,
    transport,
    watch: {source: watch.context.source, renderer: async () => watchRenderer, available: true},
  };
  return {context};
}

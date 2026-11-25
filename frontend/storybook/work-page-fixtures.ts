import type {FactoryTransport} from '../factory/api/transport.js';
import type {WorkOverviewContext} from '../factory/work/progress.js';
import {activityStoryPeer} from './activity-fixtures.js';
import {progressStoryPeer} from './progress-fixtures.js';
import {repairStoryPeer} from './repair-fixtures.js';

/** The Work overview over one transport that routes the activity and repair paths to their
 * fixtures. `calls` counts what each fixture received, so unavailable panels can prove silence. */
export function workPageStoryPeer(panels: 'available' | 'unavailable') {
  const progress = progressStoryPeer('ready'),
    activity = activityStoryPeer('recorded'),
    repair = repairStoryPeer('unfunded');
  const transport: FactoryTransport = {
    get generation() {
      return progress.overview.transport.generation;
    },
    assetBase: '/',
    request(path, init) {
      if (path.endsWith('/factory/activity')) return activity.context.transport.request(path, init);
      if (path.endsWith('/factory/repair')) return repair.context.transport.request(path, init);
      return progress.overview.transport.request(path, init);
    },
  };
  const available = panels === 'available';
  const context: WorkOverviewContext = {
    ...progress.overview,
    transport,
    activityAvailable: available,
    repairFundingAvailable: available,
    ...(available ? {commandScope: activity.context.commandScope!, presentItem: activity.context.presentItem!} : {}),
  };
  return {context, calls: {activity: activity.calls, repair: repair.calls, progress: progress.calls}};
}

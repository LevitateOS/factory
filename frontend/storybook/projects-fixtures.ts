import type {FactoryProject, FactoryProjectsPort} from '../factory/projects/projects.js';

const asOf = 1732528800;
const row = (key: string, name: string, connection: FactoryProject['connection'], summary?: FactoryProject['summary'], account = 'alex'): FactoryProject => ({
  key, name, accountLabel: account, provider: 'GitHub', connection, ...(summary ? {summary} : {}), asOf,
});

/** One row in each state: current with a need, connected, paused, not connected,
 * sign in, and one without a summary. */
export const projectStoryRows: readonly FactoryProject[] = [
  row('7', 'acme/factory', 'current', {
    needsYou: 2,
    working: [{role: 'builder', issue: '13'}, {role: 'architect'}],
    dispatch: 'open',
    latestPost: {id: 'p1', title: 'Search is taking shape', actor: 'builder', at: asOf - 3600},
  }),
  row('8', 'acme/website', 'connected', {
    needsYou: 1,
    working: [{role: 'factory', issue: '4'}],
    dispatch: 'open',
    latestPost: {id: 'p2', title: 'Waiting on allowance', actor: 'factory', at: asOf - 90000},
  }),
  row('9', 'alex/notes', 'connected', {needsYou: 0, working: [], dispatch: 'closed', latestPost: {id: 'p3', title: 'Dispatch paused until Monday', actor: 'jev', author: 'Alex'}}),
  row('10', 'alex/archive', 'not_connected', {needsYou: 0, working: [], dispatch: 'none'}),
  row('11', 'beta/docs', 'sign_in', {needsYou: 3, working: [], dispatch: 'open'}, 'beta-team'),
  row('12', 'gamma/lab', 'connected'),
];

/** A fake host port that counts what the UI asks of it. Each call is mirrored
 * onto `element` as `data-project-calls` so a check can read it. */
export function projectStoryPort(element: () => HTMLElement | undefined, rows: readonly FactoryProject[] = projectStoryRows) {
  const calls: string[] = [];
  const record = (call: string) => {
    calls.push(call);
    element()?.setAttribute('data-project-calls', JSON.stringify(calls));
  };
  const port: FactoryProjectsPort = {
    async list(signal, options) {
      signal.throwIfAborted();
      record(options?.refresh ? 'list:refresh' : 'list');
      return options?.refresh ? rows.map((item) => ({...item, asOf: asOf + 600})) : rows;
    },
    async open(key, where) {
      record('open:' + key + ':' + where);
    },
    async reconnect(key) {
      record('reconnect:' + key);
    },
    async disconnect(key) {
      record('disconnect:' + key);
    },
    async signIn(key) {
      record('signIn:' + key);
    },
  };
  return {port, calls};
}

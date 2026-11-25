import '../factory/work/runtime-controller.js';
import type {Core} from '../desktop/transport.js';
import {mountSignIn} from '../desktop/sign-in.js';
import {SodaFactoryApp} from '../factory/app/factory-app.js';

/** The guided first run, one story state per current step: 'starting' (runtime
 * not ready), 'muse' (nothing signed in), 'github' (Muse done, a GitHub code
 * waiting for approval), 'project' (Muse and GitHub done). 'saved': saved
 * GitHub and Fountain accounts, nothing in progress. */
export type SignInStoryState = 'starting' | 'muse' | 'github' | 'project' | 'saved';

const muse = {
  attempt: 1,
  available: true,
  can_control: true,
  checking: false,
  phase: 'idle',
  prompt: null,
  error: null,
  tools: {state: 'ready', upstream_version: '1.4.2', selected_version: '1.4.2', checked_at_unix: null, error: null},
};
const savedAccounts = [
  {connection: 'c1', provider: 'github', origin: 'https://github.com', account: '1', label: 'alex', state: 'stored', grant: {refresh_available: false, scope: 'repo,workflow'}},
  {connection: 'c2', provider: 'github', origin: 'https://github.com', account: '2', label: 'alex-work', state: 'expired', grant: {refresh_available: true, scope: 'repo'}},
  {connection: 'c3', provider: 'fountain', origin: 'https://fountain.example', account: '3', label: 'alex', state: 'stored', purpose: 'issues', grant: {scope: 'issues', refresh_available: false}},
];

/** Declared Rust replies for the three sign-in controllers. Status reads only;
 * actions are refused so a story never pretends a sign-in happened. */
function signInCore(state: SignInStoryState): Pick<Core, 'invoke'> {
  const replies: Record<string, unknown> = {
    muse_sign_in_status: state === 'github' || state === 'project' ? {...muse, phase: 'complete'} : muse,
    github_sign_in_status:
      state === 'github'
        ? {attempt: 1, phase: 'waiting', error: null, prompt: {user_code: 'WDJB-MJHT', verification_uri: 'https://github.com/login/device', expires_at: Math.floor(Date.now() / 1000) + 840}}
        : {attempt: 0, phase: 'idle', prompt: null, error: null},
    fountain_sign_in_status: {attempt: 0, phase: 'idle', prompt: null, error: null},
    credential_status: state === 'saved' ? savedAccounts : state === 'project' ? savedAccounts.slice(0, 1) : [],
  };
  return {
    async invoke<T>(command: string) {
      if (!(command in replies)) throw 'This story does not perform sign-in actions.';
      return structuredClone(replies[command]) as T;
    },
  };
}

/** The desktop sign-in controllers and views, placed into `root` as the desktop does. */
export function signInStoryPlacement(root: HTMLElement, state: SignInStoryState) {
  const signIn = mountSignIn(root, document.createElement('div'));
  void signIn.start(signInCore(state))(state !== 'starting', 1);
  return signIn.placement;
}

/** What the desktop shows before any connection: its sign-in region above the
 * frame, which has no context. */
export function disconnectedSignInStory(scheme: 'light' | 'dark', state: SignInStoryState = 'github') {
  const root = document.createElement('section');
  root.className = 'soda-factory-root soda-factory-accounts';
  root.dataset.factoryScheme = scheme;
  root.setAttribute('aria-label', 'Accounts');
  signInStoryPlacement(root, state);
  const app = new SodaFactoryApp();
  app.scheme = scheme;
  const page = document.createElement('div');
  page.append(root, app);
  return page;
}

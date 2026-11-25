import type {FactoryHostSection} from '../factory/app/browser.js';
import type {Core} from './transport.js';
import {GitHubAccounts, SodaDesktopGitHub} from './accounts.js';
import type {Account} from './accounts.js';
import {MuseLogin, SodaDesktopMuse} from './muse-login.js';
import {FountainLogin, SodaDesktopFountain} from './fountain-login.js';
import {SodaDesktopFirstRun} from './first-run.js';

/** The window's sign-in: one view per provider, placed in `root` until the
 * frame can show them. `start` creates the providers' controllers and returns
 * the refresh that follows runtime status. Controllers react to the window
 * being hidden in creation order, so the desktop starts them after the
 * connection panel, whose hide handling runs first. */
export function mountSignIn(root: HTMLElement, connection: HTMLElement) {
  const museView = new SodaDesktopMuse(),
    githubView = new SodaDesktopGitHub(),
    fountainView = new SodaDesktopFountain();
  const placement = new SignInPlacement(root, [
    {id: 'muse', title: 'Muse', view: museView},
    {id: 'github', title: 'GitHub', view: githubView},
    {id: 'fountain', title: 'Fountain', view: fountainView},
    {id: 'connection', title: 'Add a project', view: connection, page: 'projects'},
  ]);
  return {
    placement,
    start(core: Pick<Core, 'invoke'>, accounts?: (accounts: readonly Account[] | undefined) => void) {
      const github = new GitHubAccounts(core, accounts),
        muse = new MuseLogin(core),
        fountain = new FountainLogin(core);
      placement.firstRun.follow([github, muse]);
      placement.firstRun.facts = () => ({
        runtime: github.ready && muse.ready,
        museSignedIn: muse.view?.phase === 'complete',
        github: github.accountsCurrent
          ? github.accounts.filter((account) => account.state === 'stored').map((account) => account.label ?? account.account)
          : [],
      });
      museView.controller = muse;
      githubView.controller = github;
      fountainView.controller = fountain;
      return (ready: boolean, generation: number) =>
        Promise.all([github.refresh(ready, generation), muse.refresh(ready, generation), fountain.refresh(ready, generation)]);
    },
  };
}

interface Provider {
  readonly id: string;
  readonly title: string;
  readonly view: HTMLElement;
  /** The app page that shows it while the frame is usable. */
  readonly page: 'accounts' | 'projects';
  /** Its section in the desktop's own sign-in region. */
  readonly home: HTMLElement;
  /** Its section on its mounted page. */
  slot?: HTMLElement | undefined;
}

/** Places each provider's single sign-in view. While the app frame has a
 * context and accepts input, the views live on their page (Accounts, or Projects
 * for the account and repository picker); otherwise
 * (first run, disconnected, unavailable or suspended) they live in the desktop's
 * own sign-in region outside the frame, so sign-in never depends on a
 * connection. Each provider has one controller and one view, so its ids stay
 * unique and no sign-in is driven from two places. */
export class SignInPlacement {
  /** Host sections for the Accounts page (sign-ins) and the Projects page
   * (the account and repository picker). */
  readonly sections: {readonly accounts: readonly FactoryHostSection[]; readonly projects: readonly FactoryHostSection[]};
  /** The guided first run laid out in the desktop's own region. */
  readonly firstRun = new SodaDesktopFirstRun();
  private usable = false;
  private connecting = false;
  private readonly providers: Provider[];

  constructor(
    private readonly root: HTMLElement,
    views: readonly {id: string; title: string; view: HTMLElement; page?: 'accounts' | 'projects'}[]
  ) {
    this.providers = views.map(({id, title, view, page = 'accounts'}) => {
      const home = document.createElement('section'),
        heading = document.createElement('h2');
      heading.id = id + '-heading';
      heading.textContent = title;
      home.setAttribute('aria-labelledby', heading.id);
      home.append(heading);
      return {id, title, view, page, home};
    });
    this.firstRun.homes = Object.fromEntries(this.providers.map((provider) => [provider.id, provider.home]));
    root.replaceChildren(this.firstRun);
    const sections = (page: Provider['page']) =>
      this.providers
        .filter((provider) => provider.page === page)
        .map((provider) => ({id: provider.id, title: provider.title, mount: (signal: AbortSignal) => this.mount(provider, signal)}));
    this.sections = {accounts: sections('accounts'), projects: sections('projects')};
    this.place();
  }
  /** Whether the app frame has a context and accepts input, and whether a
   * project is opening, when setup stays out of the way. */
  frame(usable: boolean, connecting = false) {
    if (usable === this.usable && connecting === this.connecting) return;
    this.usable = usable;
    this.connecting = connecting;
    this.place();
  }
  private mount(provider: Provider, signal: AbortSignal) {
    const slot = document.createElement('div');
    provider.slot = slot;
    signal.addEventListener(
      'abort',
      () => {
        if (provider.slot === slot) provider.slot = undefined;
        this.place();
      },
      {once: true}
    );
    this.place();
    return slot;
  }
  private place() {
    for (const provider of this.providers) {
      const target = this.usable && provider.slot ? provider.slot : provider.home;
      if (provider.view.parentNode !== target) target.append(provider.view);
    }
    this.root.hidden = this.usable || this.connecting;
  }
}

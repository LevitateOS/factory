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
export function mountSignIn(root: HTMLElement) {
  const museView = new SodaDesktopMuse(),
    githubView = new SodaDesktopGitHub(),
    fountainView = new SodaDesktopFountain();
  const placement = new SignInPlacement(root, [
    {id: 'muse', title: 'Muse', view: museView},
    {id: 'github', title: 'GitHub', view: githubView},
    {id: 'fountain', title: 'Fountain', view: fountainView},
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
  /** Its section in the desktop's own sign-in region. */
  readonly home: HTMLElement;
  /** Its section on a mounted Accounts page. */
  slot?: HTMLElement | undefined;
}

/** Places each provider's single sign-in view. While the app frame has a
 * context and accepts input, the views live on its Accounts page; otherwise
 * (first run, disconnected, unavailable or suspended) they live in the desktop's
 * own sign-in region outside the frame, so sign-in never depends on a
 * connection. Each provider has one controller and one view, so its ids stay
 * unique and no sign-in is driven from two places. */
export class SignInPlacement {
  /** The Accounts page's host sections. */
  readonly sections: readonly FactoryHostSection[];
  /** The guided first run laid out in the desktop's own region. */
  readonly firstRun = new SodaDesktopFirstRun();
  private usable = false;
  private readonly providers: Provider[];

  constructor(
    private readonly root: HTMLElement,
    views: readonly {id: string; title: string; view: HTMLElement}[]
  ) {
    this.providers = views.map(({id, title, view}) => {
      const home = document.createElement('section'),
        heading = document.createElement('h2');
      heading.id = id + '-heading';
      heading.textContent = title;
      home.setAttribute('aria-labelledby', heading.id);
      home.append(heading);
      return {id, title, view, home};
    });
    this.firstRun.homes = Object.fromEntries(this.providers.map((provider) => [provider.id, provider.home]));
    root.replaceChildren(this.firstRun);
    this.sections = this.providers.map((provider) => ({
      id: provider.id,
      title: provider.title,
      mount: (signal) => this.mount(provider, signal),
    }));
    this.place();
  }
  /** Whether the app frame has a context and accepts input. */
  frame(usable: boolean) {
    if (usable === this.usable) return;
    this.usable = usable;
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
    this.root.hidden = this.usable;
  }
}

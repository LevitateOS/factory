/** Product destinations are resolved by the host; components never build forge URLs. */
export interface FactoryRepositoryLinks {
  readonly repository: string;
  feed(post?: string): string;
  request(id: string): string;
  issue(number: string | number): string | undefined;
  pull(number: string | number): string | undefined;
  factoryStatus(issue?: string | number): string;
  app?(delivery?: string): string;
  knowledge?(item?: string): string;
  settings?(section: string): string;
}

/** Recorded work views consume only host-admitted issue and pull destinations. */
export type FactoryWorkLinks = Pick<FactoryRepositoryLinks, 'issue' | 'pull'>;

/** Review destinations, including comment addressing, belong to the host. */
export interface FactoryAcceptanceLinks {
  issue(number: string): string | undefined;
  comment(issue: string, comment: string): string | undefined;
}

export interface FactoryRepositoryCatalog {
  readonly refs: Map<string, {path: string | undefined; label: string}>;
  /** The host's catalogue traversal completed; this is not remote discovery coverage. */
  readonly listedAll: boolean;
}

/** Host integration for the feed. Repository IDs are local Factory ledger IDs. */
export interface FactoryFeedHost {
  /** Profile, provider instance and acting account scope; never a credential. */
  readonly draftScope: string;
  repositories(wanted: readonly string[], signal: AbortSignal): Promise<FactoryRepositoryCatalog>;
  links(repository: string, label: string): FactoryRepositoryLinks | undefined;
  applicationOrigins?(signal: AbortSignal): Promise<ReadonlyMap<string, string>>;
  navigate(path: string): void;
}

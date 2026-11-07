import {
  readExplorationCommand,
  retainExploration,
  settleExploration,
  explorationResult,
  explorationReceipt,
} from './exploration-record.js';
import type {PendingExploration, ExplorationResult} from './exploration-record.js';
import {
  readPlanning,
  retainPlanning,
  bindPlanning,
  settlePlanning,
  planningReceipt,
  planningLaunch,
} from './planning-record.js';
import type {PendingPlanning} from './planning-record.js';
import {LitElement, html, css} from 'lit';
import type {PropertyValues} from 'lit';
import {object, check, id, SodaRequestError} from '../api/models.js';
import {readGeneration, retainGeneration, settleGeneration, generationReceipt} from './generation-record.js';
import type {PendingGeneration, GenerationReceipt} from './generation-record.js';
import {readSave, retainSave, settleSave, savedReceipt, savedDraft, releaseObsoleteSave} from './save-record.js';
import type {PendingSave} from './save-record.js';
import {readRequestDraft, retainRequestDraft, requestDraftKey} from './draft.js';
import type {RequestDraft, RequestDraftAddress, RequestDraftRead, ShelvedDepRepair} from './draft.js';
import {TaskShelf} from './task-shelf.js';
import {
  decisionID,
  department,
  departments,
  exploration as parseExploration,
  intentGeneration,
  intentPlanStatus,
  intentRevision,
  planActionBody,
  planWait,
  generationWaitNotice,
  generationFailureNotice,
  planningFailedNotice,
  planningCancelledNotice,
  runPlanWork,
  newDecisionID,
  savedReviewRead,
} from '../api/request-response.js';
import type {
  Department,
  Exploration,
  IntentGenerationStatus,
  IntentPlanStatus,
  IntentRevision,
  IntentTask,
  ProductIdea,
  SavedReviewRead,
} from '../api/request-response.js';
import {
  describeIntent,
  reviewIntent,
  departmentDialog,
  reviewReady,
  attentionText,
  plannerStopped,
} from './request-view.js';
import type {IntentView} from './request-view.js';

export type IntentAPI = (path: string, body?: Record<string, unknown>, signal?: AbortSignal) => Promise<unknown>;
export class SodaIntent extends LitElement {
  static properties = {
    api: {attribute: false, noAccessor: true},
    blocked: {type: Boolean, noAccessor: true},
    active: {type: Boolean, noAccessor: true},
    actor: {type: String, noAccessor: true},
    draftScope: {type: String, noAccessor: true},
    request: {type: String, noAccessor: true},
    project: {type: String},
    exploration: {state: true},
    objective: {state: true},
    department: {state: true},
    departments: {state: true},
    revision: {state: true},
    busy: {state: true},
    busyNote: {state: true},
    dirty: {state: true},
    explorationCommandNotice: {state: true},
    explorationCanResume: {state: true},
    explorationCanRelease: {state: true},
    planningCommandNotice: {state: true},
    planningCanResume: {state: true},
    planningCanRelease: {state: true},
    generationCommandNotice: {state: true},
    generationCanResume: {state: true},
    generationCanRelease: {state: true},
    saveNotice: {state: true},
    saveRetry: {state: true},
    saveObsolete: {state: true},
    draftNotice: {state: true},
    draftPreview: {state: true},
    error: {state: true},
    departmentDialog: {state: true},
    departmentName: {state: true},
    departmentDescription: {state: true},
    changeOpen: {state: true},
    changeText: {state: true},
    generation: {state: true},
    generationError: {state: true},
    generationNotice: {state: true},
    maxIssues: {state: true},
    planOperation: {state: true},
    plan: {state: true},
    planNotice: {state: true},
    planIntent: {state: true},
  };
  static styles = css`
    :host {
      display: block;
      width: 100%;
      font: var(--soda-font-body, 16px sans-serif);
      color: var(--soda-page-text, inherit);
    }
    :host([hidden]) {
      display: none;
    }
    section {
      max-width: 720px;
      margin: auto;
    }
    .intent-review,
    .intent-entry {
      text-align: left;
    }
    .intent-entry {
      padding-block: clamp(0.5rem, 3vh, 2rem);
    }
    h2 {
      font: var(--soda-font-title, 32px sans-serif);
      margin: 0 0 1.5rem;
    }
    h3 {
      font: var(--soda-font-heading, 20px sans-serif);
    }
    .state-chip {
      width: fit-content;
      margin: -1rem 0 1rem;
      padding: 0.125rem 0.5rem;
      border: 1px solid var(--soda-page-edge);
      color: var(--soda-page-muted);
      font-size: 0.875rem;
    }
    label {
      display: block;
    }
    textarea,
    input,
    select,
    button {
      box-sizing: border-box;
      font: inherit;
      color: inherit;
      border: 1px solid var(--soda-page-edge);
      background: var(--soda-page-surface);
      border-radius: 0;
      padding: 0.75rem;
    }
    textarea {
      width: 100%;
      resize: vertical;
    }
    input,
    select {
      display: block;
      max-width: 100%;
      margin-block: 0.5rem;
    }
    button {
      cursor: pointer;
      min-height: 44px;
    }
    button:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .primary {
      background: var(--soda-button-primary-bg);
      color: var(--soda-button-primary-text);
    }
    button.primary:disabled {
      opacity: 1;
      background: var(--soda-page-hover);
      color: var(--soda-page-muted);
      border-color: var(--soda-page-edge);
      cursor: not-allowed;
    }
    .context,
    .actions {
      display: flex;
      align-items: end;
      flex-wrap: wrap;
      gap: 1rem;
      margin-block: 1rem;
    }
    .context label {
      flex: 1;
    }
    .bounds {
      margin-block: 1rem;
    }
    article {
      border-block-end: 1px solid var(--soda-page-edge);
      padding-block: 1rem;
    }
    .consequence {
      color: var(--soda-page-muted);
    }
    .department-dialog {
      box-sizing: border-box;
      position: fixed;
      z-index: 40;
      inset: 15% max(1rem, calc((100vw - 600px)/2)) auto;
      padding: 1.5rem;
      background: var(--soda-page-surface);
      border: 1px solid var(--soda-page-edge);
      box-shadow: var(--soda-elevation-dialog);
      max-height: 70vh;
      overflow: auto;
    }
    .department-dialog input {
      width: 100%;
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
    }
    [role='status'] {
      margin-block: 1rem;
    }
    [role='status'].intent-live-status {
      margin: 0;
    }
    .intent-live-status > span {
      display: block;
      margin-block: 1rem;
    }
    .scope-gate {
      border: 1px solid var(--soda-page-edge);
      padding: 1rem;
      margin-block: 1rem;
    }
    :is(button, input, select, textarea, summary, a):focus-visible {
      outline: 2px solid var(--soda-page-focus);
      outline-offset: 2px;
    }
    h2[tabindex='-1']:focus,
    h3[tabindex='-1']:focus {
      outline: none;
    }
  `;
  private apiValue: IntentAPI | undefined = undefined;
  private blockedValue: boolean = false;
  private activeValue: boolean = false;
  private actorValue: string = '';
  private draftScopeValue: string = '';
  private requestValue: string = '';
  declare project: string;
  declare exploration: Exploration | undefined;
  declare objective: string;
  declare department: string;
  declare departments: Department[];
  declare revision: IntentRevision | undefined;
  declare busy: boolean;
  declare busyNote: string;
  declare dirty: boolean;
  declare private draftNotice: string;
  declare private draftPreview: RequestDraft | undefined;
  private draftReads = new Map<string, {raw: string | undefined; protect: boolean; owned: boolean}>();
  declare error: string;
  declare departmentDialog: boolean;
  declare departmentName: string;
  declare departmentDescription: string;
  declare changeOpen: boolean;
  declare changeText: string;
  declare generation: IntentGenerationStatus | undefined;
  declare generationError: string;
  declare maxIssues: number;
  declare planOperation: string;
  declare plan: IntentPlanStatus | undefined;
  declare planNotice: string;
  declare planIntent: string;
  declare private explorationCommandNotice: string;
  declare private explorationCanResume: boolean;
  declare private explorationCanRelease: boolean;
  declare private planningCommandNotice: string;
  declare private planningCanResume: boolean;
  declare private planningCanRelease: boolean;
  declare private generationCommandNotice: string;
  declare private generationCanResume: boolean;
  declare private generationCanRelease: boolean;
  declare private saveNotice: string;
  declare private saveRetry: boolean;
  declare private saveObsolete: boolean;
  private identityUpdating = false;
  private requestEpoch = 0;
  private requestLifetime = new AbortController();
  private operation: AbortController | undefined;
  private resumeController: AbortController | undefined;
  private restoredReview: RestoredReviewIdentity | undefined;
  private resumeID = '';
  private loadedRequest = '';
  private generationIntent = '';
  private savedSnapshot: IntentRevision | undefined;
  private scopeGateArmed = false;
  private taskShelf = new TaskShelf();
  // Cross-device saved-history recovery (saved bytes only): the local shelf
  // above is same-browser localStorage, so a resume on another device starts
  // with no shelf. Recovery then reads ONLY the owner-private history
  // endpoint (bare GET .../history, default page): the exact predecessor
  // named by the current head, or a bounded exact walk back along the
  // predecessor chain. Unsent edits are never in history and stay
  // local-only; in-memory edges never existed in a predecessor and are never
  // treated as history. Entries stage per declined idea and only a user
  // re-accept moves them into the shelf for the next save; nothing is
  // unioned and no decision is ever changed silently.
  private savedHistory: SavedHistoryEntry[] = [];
  private savedHistoryNote = '';
  private savedHistorySkipped = 0;
  private savedHistoryHead = '';
  private departmentsLoaded = false;
  private saveStorageChanged = (event: StorageEvent) => {
    if (
      event.key === null ||
      event.key.startsWith('soda-intent-save:') ||
      event.key.startsWith('soda-intent-generation:') ||
      event.key.startsWith('soda-intent-planning:') ||
      event.key.startsWith('soda-intent-exploration:')
    )
      this.requestUpdate();
  };
  private progressTimer?: ReturnType<typeof setInterval>;
  private progressReading = false;
  declare generationNotice: string;
  constructor() {
    super();
    this.blocked = false;
    this.active = false;
    this.actor = '';
    this.draftScope = '';
    this.request = '';
    this.draftNotice = '';
    this.project = '';
    this.exploration = undefined;
    this.objective = '';
    this.department = '';
    this.departments = [];
    this.busy = this.dirty = this.departmentDialog = false;
    this.busyNote = '';
    this.error = this.departmentName = this.departmentDescription = this.changeText = '';
    this.changeOpen = false;
    this.generation = undefined;
    this.generationError = '';
    this.explorationCommandNotice = this.planningCommandNotice = this.generationCommandNotice = '';
    this.saveNotice = this.generationNotice = '';
    this.explorationCanResume = this.explorationCanRelease = this.planningCanResume = this.planningCanRelease = false;
    this.generationCanResume = this.generationCanRelease = this.saveRetry = this.saveObsolete = false;
    this.savedSnapshot = undefined;
    this.maxIssues = 20;
    this.planOperation = 'propose_intent';
    this.plan = undefined;
    this.planNotice = '';
    this.planIntent = '';
  }
  get api(): IntentAPI | undefined {
    return this.apiValue;
  }
  set api(value: IntentAPI | undefined) {
    const previous = this.apiValue;
    if (previous === value) return;
    this.retireRequests();
    this.apiValue = value;
    this.requestUpdate('api', previous);
  }
  get active(): boolean {
    return this.activeValue;
  }
  set active(value: boolean) {
    const previous = this.activeValue;
    if (previous === value) return;
    this.retireRequests();
    this.activeValue = value;
    this.requestUpdate('active', previous);
  }
  get blocked(): boolean {
    return this.blockedValue;
  }
  set blocked(value: boolean) {
    const previous = this.blockedValue;
    if (previous === value) return;
    this.retireRequests();
    this.blockedValue = value;
    this.requestUpdate('blocked', previous);
  }
  get actor(): string {
    return this.actorValue;
  }
  set actor(value: string) {
    const previous = this.actorValue;
    if (previous === value) return;
    this.identityUpdating = true;
    this.retireRequests();
    this.actorValue = value;
    this.requestUpdate('actor', previous);
  }
  get draftScope(): string {
    return this.draftScopeValue;
  }
  set draftScope(value: string) {
    const previous = this.draftScopeValue;
    if (previous === value) return;
    this.identityUpdating = true;
    this.retireRequests();
    this.draftScopeValue = value;
    this.requestUpdate('draftScope', previous);
  }
  get request(): string {
    return this.requestValue;
  }
  set request(value: string) {
    const previous = this.requestValue;
    if (previous === value) return;
    // A committed save updates its own route while the same action continues.
    if (!this.operation || this.resumeController || value !== this.revision?.intent) {
      this.retireRequests();
      this.explorationCommandNotice = '';
      this.explorationCanResume = this.explorationCanRelease = false;
      this.planningCommandNotice = '';
      this.planningCanResume = this.planningCanRelease = false;
      this.generationCommandNotice = '';
      this.generationCanResume = this.generationCanRelease = false;
      this.saveNotice = '';
      this.saveRetry = this.saveObsolete = false;
    }
    this.requestValue = value;
    this.requestUpdate('request', previous);
  }
  private retireRequests() {
    this.requestEpoch++;
    this.requestLifetime.abort();
    this.requestLifetime = new AbortController();
    this.operation?.abort();
    this.resumeController?.abort();
    this.operation = this.resumeController = undefined;
    this.resumeID = '';
    this.busy = false;
    this.progressReading = false;
  }
  private requestLive(epoch: number): boolean {
    return (
      !this.identityUpdating &&
      epoch === this.requestEpoch &&
      this.active &&
      !this.blocked &&
      !!this.api &&
      this.isConnected
    );
  }
  protected willUpdate(changed: PropertyValues<this>) {
    if (changed.has('actor') || changed.has('draftScope')) {
      this.explorationCommandNotice = '';
      this.explorationCanResume = this.explorationCanRelease = false;
      this.saveNotice = '';
      this.saveRetry = false;
      this.saveObsolete = false;
      this.draftReads.clear();
      this.draftNotice = '';
      this.draftPreview = undefined;
      this.operation?.abort();
      this.resumeController?.abort();
      this.operation = this.resumeController = undefined;
      this.busy = false;
      this.loadedRequest = '';
      this.revision = this.savedSnapshot = undefined;
      this.departmentsLoaded = false;
      this.error = '';
      this.departments = [];
      this.dirty = this.changeOpen = this.departmentDialog = false;
      this.scopeGateArmed = false;
      this.taskShelf.reset();
      this.savedHistory = [];
      this.savedHistoryNote = '';
      this.savedHistorySkipped = 0;
      this.savedHistoryHead = '';
      this.plan = this.generation = undefined;
      this.planIntent = this.generationIntent = '';
      this.planNotice = this.generationNotice = this.generationError = '';
      this.objective = '';
      this.department = '';
      this.changeText = '';
      this.restoreDraft();
      this.exploration = undefined;
      this.restoredReview = undefined;
      this.identityUpdating = false;
    }
  }
  connectedCallback() {
    super.connectedCallback();
    window.addEventListener('storage', this.saveStorageChanged);
    this.requestUpdate('active', undefined);
    this.progressTimer = setInterval(() => void this.refreshProgress(), 10000);
  }
  private async refreshProgress() {
    const epoch = this.requestEpoch;
    const revision = this.revision,
      actor = this.actor,
      draftScope = this.draftScope;
    if (
      !revision ||
      !this.requestLive(epoch) ||
      !this.active ||
      this.blocked ||
      this.busy ||
      this.progressReading ||
      document.visibilityState !== 'visible'
    )
      return;
    const accepted = revision.state === 'accepted';
    if (!accepted && (revision.state !== 'draft' || revision.repository === '0')) return;
    this.progressReading = true;
    try {
      const value = await this.call(
        '/api/factory/intents/' + revision.intent + (accepted ? '/generation' : '/plan'),
        undefined,
        AbortSignal.timeout(15000)
      );
      if (
        !this.requestLive(epoch) ||
        this.actor !== actor ||
        this.draftScope !== draftScope ||
        this.revision?.id !== revision.id
      )
        return;
      if (accepted) {
        this.generation = intentGeneration(value);
        this.generationError = '';
      } else {
        this.applyPolledPlanStatus(intentPlanStatus(value), revision.id);
      }
    } catch {
      if (
        this.requestLive(epoch) &&
        this.actor === actor &&
        this.draftScope === draftScope &&
        this.revision?.id === revision.id
      ) {
        if (accepted)
          this.generationError = 'Live updates interrupted. Your saved work is kept; retrying automatically.';
        else if (this.planNotice === '')
          this.planNotice = 'Live updates interrupted. Your saved work is kept; retrying automatically.';
      }
    } finally {
      if (epoch === this.requestEpoch) this.progressReading = false;
    }
  }
  // One polled-status contract for the timer and explicit refresh: a poll
  // never erases the guidance behind the current draft. Terminal states
  // always restate their guidance (a stale running text must not survive
  // a settled run); live states update guidance only on transition; a
  // missing plan keeps whatever the last explicit action reported.
  private applyPolledPlanStatus(status: IntentPlanStatus, id: string) {
    if (status.revision !== id || this.revision?.id !== id) return;
    const previous = this.plan?.revision === id ? this.plan.state : undefined;
    this.plan = status;
    // A repeated state never erases the current guidance, so a 202 wait or
    // an explicit error survives polls. A genuine transition restates it,
    // so stale waits/failures cannot survive work starting or finishing.
    if (status.state === 'none' || previous === status.state) return;
    if (status.state === 'failed' || status.state === 'cancelled') {
      this.planNotice = status.state === 'failed' ? planningFailedNotice : planningCancelledNotice;
    } else if (status.state === 'completed') this.planNotice = '';
    else if (status.state === 'pending')
      this.planNotice = 'Your request is saved. Its planning progress and any question appear here.';
    else if (status.state === 'attention') this.planNotice = attentionText(status);
  }
  private draftAddress(): RequestDraftAddress {
    return {scope: this.draftScope, actor: this.actor, subject: this.revision?.intent || this.request || 'new'};
  }
  private draftProblem(read: RequestDraftRead) {
    this.draftPreview = read.persisted;
    this.draftNotice =
      read.problem === 'foreign' || read.problem === 'invalid'
        ? 'Saved draft ownership or contents could not be verified. Its bytes were preserved. Current writing stays in this browser; copy it before leaving.'
        : read.problem === 'conflict'
          ? 'Another saved draft changed. Both copies were preserved. Current writing is kept in this browser; copy it before leaving.'
          : read.problem === 'unavailable' || !read.durable
            ? 'The draft could not be saved durably. Current writing is kept in this browser; copy it before closing or reloading.'
            : '';
  }
  private captureDraft(): RequestDraft {
    return structuredClone({
      base: this.revision?.id ?? '',
      repository: this.revision?.repository ?? '0',
      objective: this.objective,
      department: this.department,
      maxIssues: this.maxIssues,
      changeText: this.changeText,
      ideas:
        this.revision?.ideas.map(({id, decision, correction}) => ({
          id,
          decision,
          ...(correction === undefined ? {} : {correction}),
        })) ?? [],
      ...(this.revision?.approach === undefined ? {} : {approach: this.revision.approach}),
      ...this.taskShelf.snapshot(),
    });
  }
  private rememberDraft() {
    if (!this.actorValid() || this.identityUpdating) return;
    try {
      const address = this.draftAddress(),
        key = requestDraftKey(address);
      let observed = this.draftReads.get(key);
      if (!observed) {
        const read = readRequestDraft(address);
        // A newly encountered nonempty copy has not been reviewed by this view.
        observed = {raw: read.raw, protect: !!read.draft, owned: false};
        this.draftReads.set(key, observed);
      }
      if (observed.protect && !observed.owned && !readRequestDraft(address).durable) {
        this.draftPreview = readRequestDraft(address).draft;
        this.draftNotice =
          'An unreviewed local draft was preserved. Current writing stays here; copy it before leaving.';
        return;
      }
      const draft = this.captureDraft();
      const retained = retainRequestDraft(address, draft, observed.raw, observed.protect);
      if (retained.raw !== observed.raw) this.draftReads.set(key, {...observed, raw: retained.raw, owned: true});
      const read = readRequestDraft(address);
      this.draftProblem(retained.status === 'conflict' && !read.problem ? {...read, problem: 'conflict'} : read);
      if (retained.status === 'invalid')
        this.draftNotice =
          'The current draft could not be encoded safely. Current writing stays here; copy it before leaving.';
    } catch {
      this.draftPreview = undefined;
      this.draftNotice =
        'Draft identity or storage could not be read. Current writing stays here; copy it before leaving.';
    }
  }
  private restoreDraft() {
    if (!this.actorValid()) return;
    try {
      const address = this.draftAddress(),
        key = requestDraftKey(address),
        read = readRequestDraft(address);
      this.draftReads.set(key, {raw: read.raw, protect: false, owned: true});
      this.draftProblem(read);
      const archive = this.savedSnapshot && savedDraft(address, this.savedSnapshot);
      if (archive) {
        this.taskShelf.restore(archive);
      }
      const draft = read.draft;
      if (!draft) return;
      const ideas = new Set((this.revision?.ideas ?? []).map((idea) => idea.id));
      if (
        draft.base !== (this.revision?.id ?? '') ||
        draft.repository !== (this.revision?.repository ?? '0') ||
        draft.ideas.length !== ideas.size ||
        !draft.ideas.every((idea) => ideas.has(idea.id))
      ) {
        this.draftReads.set(key, {raw: read.raw, protect: true, owned: false});
        this.draftPreview = read.persisted ?? draft;
        this.draftNotice =
          'A saved draft belongs to another revision or repository. It was preserved; current writing was kept.';
        return;
      }
      // Validate the entire record before applying any scalar, review or shelf field.
      this.objective = draft.objective;
      this.department = draft.department;
      this.maxIssues = draft.maxIssues;
      this.changeText = draft.changeText;
      if (this.revision) {
        this.revision = {
          ...this.revision,
          ideas: this.revision.ideas.map((idea) => {
            const saved = draft.ideas.find((row) => row.id === idea.id)!;
            return saved.decision === idea.decision && saved.correction === idea.correction
              ? idea
              : this.decidedIdea(idea, saved.decision, saved.correction);
          }),
          ...(draft.approach === undefined ? {} : {approach: draft.approach}),
        };
      }
      this.taskShelf.restore(draft);
      this.dirty =
        !!this.revision &&
        (this.objective !== this.revision.objective ||
          this.department !== (this.revision.department || '') ||
          this.maxIssues !== this.revision.max_issues ||
          JSON.stringify(this.revision.ideas) !== JSON.stringify(this.savedSnapshot?.ideas) ||
          this.revision.approach !== this.savedSnapshot?.approach);
    } catch {
      this.draftPreview = undefined;
      this.draftNotice =
        'Draft identity or storage could not be read. Current writing stays here; copy it before leaving.';
    }
  }
  protected updated(changed: PropertyValues<this>) {
    if (changed.has('active') || changed.has('blocked') || changed.has('api') || (changed.has('busy') && !this.busy))
      this.activate();
    if (this.resumeReady(changed)) this.resume();
    if (this.generationReady(changed)) this.fetchGeneration();
    if (this.planReady(changed)) this.fetchPlanStatus();
  }
  private planReady(changed: PropertyValues<this>) {
    return (
      changed.has('revision') ||
      changed.has('api') ||
      changed.has('active') ||
      changed.has('blocked') ||
      (changed.has('busy') && !this.busy)
    );
  }
  private generationReady(changed: PropertyValues<this>) {
    return (
      changed.has('revision') ||
      changed.has('api') ||
      changed.has('active') ||
      changed.has('blocked') ||
      (changed.has('busy') && !this.busy)
    );
  }
  private activate() {
    if (!this.requestLive(this.requestEpoch)) return;
    if (!this.departmentsLoaded) {
      void this.loadDepartments();
    }
  }
  private resumeReady(changed: PropertyValues<this>) {
    return (
      changed.has('request') ||
      changed.has('blocked') ||
      changed.has('api') ||
      changed.has('active') ||
      changed.has('draftScope') ||
      (changed.has('busy') && !this.busy)
    );
  }
  disconnectedCallback() {
    window.removeEventListener('storage', this.saveStorageChanged);
    clearInterval(this.progressTimer);
    this.retireRequests();
    super.disconnectedCallback();
  }
  reset() {
    // Persist the recovery record BEFORE clearing: reset starts a new
    // request but the saved revision still exists, so resuming it in this
    // browser must still offer the same Re-accept recovery. Shelf recovery
    // is same-browser local state only (actor/request key, this browser) —
    // never cross-device: the history endpoint retains the saved graph but
    // the UI restores the shelf from local state alone.
    this.rememberDraft();
    this.explorationCommandNotice = '';
    this.explorationCanResume = this.explorationCanRelease = false;
    this.planningCommandNotice = '';
    this.planningCanResume = this.planningCanRelease = false;
    this.generationCommandNotice = '';
    this.generationCanResume = this.generationCanRelease = false;
    this.saveNotice = '';
    this.saveRetry = false;
    this.saveObsolete = false;
    this.retireRequests();
    this.scopeGateArmed = false;
    this.taskShelf.reset();
    this.savedHistory = [];
    this.savedHistoryNote = '';
    this.savedHistorySkipped = 0;
    this.savedHistoryHead = '';
    this.operation?.abort();
    this.resumeController?.abort();
    this.request = '';
    this.loadedRequest = '';
    this.revision = undefined;
    this.savedSnapshot = undefined;
    this.exploration = undefined;
    this.restoredReview = undefined;
    this.generation = undefined;
    this.generationError = '';
    this.generationIntent = '';
    this.plan = undefined;
    this.planIntent = this.planNotice = '';
    this.objective = '';
    this.dirty = false;
    this.error = '';
    this.changeOpen = false;
    this.changeText = '';
  }
  focusReview() {
    const epoch = this.requestEpoch;
    void this.updateComplete.then(() => {
      if (!this.requestLive(epoch)) return;
      const root = this.shadowRoot;
      if (!root) return;
      const target =
        root.querySelector<HTMLElement>('section.intent-review h2') ||
        root.querySelector<HTMLElement>('section.intent-entry h2');
      target?.focus();
    });
  }
  // editRequest moves focus to the review's Desired outcome field so the
  // presenter can change the request; it saves nothing and spends no calls.
  private editRequest = () => {
    const epoch = this.requestEpoch;
    void this.updateComplete.then(() => {
      if (!this.requestLive(epoch)) return;
      const area = this.shadowRoot?.querySelector<HTMLTextAreaElement>('section.intent-review textarea');
      if (!area) return;
      area.scrollIntoView({block: 'center'});
      area.focus({preventScroll: true});
    });
  };
  focusFindings() {
    const epoch = this.requestEpoch;
    void this.updateComplete.then(() => {
      if (!this.requestLive(epoch)) return;
      this.shadowRoot
        ?.querySelector<HTMLElement>('section[aria-label="Consistency check"] h3')
        ?.focus({preventScroll: false});
    });
  }
  private async perform(operation: (signal: AbortSignal) => Promise<void>, note = 'Saving and planning…') {
    const epoch = this.requestEpoch;
    if (this.busy || !this.requestLive(epoch)) return false;
    const request = new AbortController();
    this.operation = request;
    this.busy = true;
    this.busyNote = note;
    this.error = '';
    try {
      await operation(request.signal);
      return !request.signal.aborted && this.requestLive(epoch);
    } catch {
      // A refused save sets its own explicit guidance before throwing; keep
      // it so the user knows what review is required.
      if (this.requestLive(epoch) && !request.signal.aborted && !this.error)
        this.error = 'Could not save or plan this request. Your choices are kept; try again.';
      return false;
    } finally {
      if (this.operation === request && this.requestLive(epoch)) {
        this.operation = undefined;
        this.busy = false;
        this.dispatchEvent(new CustomEvent('soda-intent-operation', {bubbles: true, composed: true}));
      }
    }
  }
  private async call(path: string, body: Record<string, unknown> | undefined, signal: AbortSignal) {
    const epoch = this.requestEpoch,
      api = this.api;
    const joined = AbortSignal.any([signal, this.requestLifetime.signal, AbortSignal.timeout(90000)]);
    joined.throwIfAborted();
    if (!api || !this.requestLive(epoch)) throw Error('Intent request retired');
    try {
      const response = await api(path, body, joined);
      joined.throwIfAborted();
      if (!this.requestLive(epoch) || this.api !== api) throw Error('Intent request retired');
      return response;
    } catch (error) {
      // Host retirement must not become a new scope's failure message or a
      // follow-up request through a replacement API. Retiring setters abort
      // the original action/resume signal as well as this joined read signal.
      joined.throwIfAborted();
      if (!this.requestLive(epoch) || this.api !== api) throw Error('Intent request retired');
      throw error;
    }
  }
  private loadDepartments() {
    return this.perform(async (signal) => {
      const value = await this.call('/api/factory/departments', undefined, signal);
      signal.throwIfAborted();
      this.departments = departments(value);
      this.departmentsLoaded = true;
    }, 'Loading…');
  }
  private resumeSuperseded(id: string): boolean {
    if (!this.resumeController) return this.busy;
    if (this.resumeID === id) return true;
    this.resumeController.abort();
    return false;
  }
  private resumeAdmitted(id: string): boolean {
    if (!id || !this.requestLive(this.requestEpoch)) return false;
    if (id === this.loadedRequest || id === this.revision?.intent) return false;
    return !this.resumeSuperseded(id);
  }
  private resume() {
    const id = this.request.trim();
    if (!this.resumeAdmitted(id)) return;
    if (!decisionID(id)) {
      this.error = 'This saved request address is invalid. Your current edits are kept.';
      return;
    }
    const controller = new AbortController();
    this.resumeController = controller;
    this.resumeID = id;
    this.busy = true;
    this.busyNote = 'Loading saved request…';
    this.error = '';
    void this.loadRequest(id, controller);
  }
  private resumeCurrent(id: string, controller: AbortController): boolean {
    return !controller.signal.aborted && this.resumeController === controller && this.request === id;
  }
  private async loadRequest(id: string, controller: AbortController) {
    try {
      const saved = intentRevision(await this.call('/api/factory/intents/' + id, undefined, controller.signal));
      if (!this.resumeCurrent(id, controller)) return;
      if (saved.intent !== id) throw Error('Mismatched intent revision');
      const read = await this.loadReview(id, controller.signal);
      if (!this.resumeCurrent(id, controller)) return;
      if (read && (read.intent !== id || read.head_revision !== saved.id)) throw Error('Mismatched review snapshot');
      this.applyRevision(saved, read);
      if (!this.resumeCurrent(id, controller)) return;
      this.loadedRequest = id;
      this.dispatchEvent(
        new CustomEvent('intent-resumed', {
          detail: {intent: saved.intent, revision: saved.id, state: saved.state, repository: saved.repository},
          bubbles: true,
          composed: true,
        })
      );
      // Land on the review heading: a resume from the saved list must not
      // leave focus on the body with the heading scrolled out of view.
      void this.updateComplete.then(() => {
        if (controller.signal.aborted || !this.active || this.loadedRequest !== id) return;
        const heading = this.shadowRoot?.querySelector<HTMLElement>('section.intent-review h2');
        if (!heading) return;
        heading.scrollIntoView({block: 'start'});
        heading.focus({preventScroll: true});
      });
    } catch (error) {
      this.resumeFailed(id, controller, error);
    } finally {
      if (this.resumeController === controller) {
        this.resumeController = undefined;
        this.resumeID = '';
        this.busy = false;
      }
    }
  }
  private generationAdmitted(revision: IntentRevision, refresh: boolean): boolean {
    if (!this.api || !this.active || this.blocked) return false;
    if (!refresh && (this.generationIntent === revision.id || this.generationError)) return false;
    return true;
  }
  private fetchGeneration(refresh = false) {
    const revision = this.revision;
    if (!revision || revision.state !== 'accepted' || !this.generationAdmitted(revision, refresh)) return;
    if (refresh) {
      this.generationIntent = '';
      this.generationError = '';
    }
    void this.loadGeneration(revision, this.error);
  }
  private loadGeneration(revision: IntentRevision, prior: string) {
    const epoch = this.requestEpoch;
    const id = revision.id;
    void this.perform(async (signal) => {
      try {
        const status = intentGeneration(
          await this.call('/api/factory/intents/' + revision.intent + '/generation', undefined, signal)
        );
        if (this.revision?.id !== id) return;
        this.generation = status;
        this.generationIntent = id;
        this.generationError = '';
      } catch {
        if (!signal.aborted)
          this.generationError = 'Factory status could not be loaded. Your direction is saved; try again.';
      }
    }, 'Loading Factory status…').then(() => {
      if (this.requestLive(epoch) && this.revision?.id === id && this.error === '') this.error = prior;
    });
  }
  private resumeFailed(id: string, controller: AbortController, error: unknown) {
    if (controller.signal.aborted || this.resumeController !== controller) return;
    this.loadedRequest = id;
    if (error instanceof SodaRequestError && error.status === 404)
      this.error = 'This saved request is unavailable to the current account. Your current edits are kept.';
    else this.error = 'Could not load the saved request. Your current edits are kept; try again.';
  }
  private async loadReview(id: string, signal: AbortSignal): Promise<SavedReviewRead | undefined> {
    try {
      return savedReviewRead(await this.call('/api/factory/intents/' + id + '/review', undefined, signal));
    } catch (error) {
      // Only an absent review degrades to no restored review. Network,
      // parse and all other failures rethrow so the resume fails with
      // unsaved edits kept instead of applying a bare revision.
      if (error instanceof SodaRequestError && error.status === 404) return undefined;
      throw error;
    }
  }
  private applyRevision(saved: IntentRevision, read?: SavedReviewRead) {
    if (saved.intent !== this.revision?.intent) {
    }
    this.revision = saved;
    this.savedSnapshot = structuredClone(saved);
    this.scopeGateArmed = false;
    this.reconcileShelvedTasks(saved);
    const adopted = adoptReview(read);
    this.exploration = adopted.exploration;
    this.restoredReview = adopted.restored;
    this.objective = saved.objective;
    this.department = saved.department || '';
    this.maxIssues = saved.max_issues;
    this.dirty = false;
    this.changeOpen = false;
    this.changeText = '';
    this.restoreDraft();
    this.maybeRecoverFromHistory();
  }
  private explorationStale(): boolean {
    const exploration = this.exploration;
    const revision = this.revision;
    if (!exploration || !revision) return false;
    if (this.restoredReview) return restoredStale(this.dirty, this.restoredReview.result, revision.id);
    if (this.dirty) return true;
    const content = exploration.content;
    if (content.intent !== undefined && content.intent !== revision.intent) return true;
    if (content.revision !== undefined && content.revision !== revision.id) return true;
    return false;
  }
  private nextRevision(state: IntentRevision['state'], repository = '0'): IntentRevision {
    const current = this.revision;
    return {
      id: newDecisionID(),
      intent: current?.intent || newDecisionID(),
      ...(current ? {predecessor: current.id} : {}),
      repository,
      ...(this.department ? {department: this.department} : {}),
      objective: this.objective.trim(),
      ...this.currentProposal(),
      state,
      max_issues: this.maxIssues,
      ...(current?.approach ? {approach: current.approach} : {}),
    };
  }
  private terminalRevision(): boolean {
    const state = this.revision?.state;
    return state === 'declined' || state === 'rejected' || state === 'abandoned';
  }
  private currentProposal() {
    this.taskShelf.clearProposal();
    if (this.terminalRevision()) return {ideas: [], tasks: []};
    if (this.objective.trim() !== this.revision?.objective) return {ideas: [], tasks: []};
    const ideas = this.revision?.ideas || [];
    const effective = this.revision?.effective_outcomes?.filter((id) =>
      ideas.some((idea) => idea.id === id && (idea.decision === 'accept' || idea.decision === 'edit'))
    );
    const tasks = this.taskShelf.proposal(ideas, this.revision?.tasks || []);
    return {
      ideas,
      tasks,
      ...(effective === undefined ? {} : {effective_outcomes: effective}),
    };
  }
  // Resolved review scope: the explicit narrowed subset when the stored
  // revision records one, else every decided idea in revision order.
  // Pending/declined ideas never resolve in.
  private resolvedScope(revision: IntentRevision): ProductIdea[] {
    const decided = (revision.ideas || []).filter((idea) => idea.decision === 'accept' || idea.decision === 'edit');
    const explicit = revision.effective_outcomes;
    if (explicit && explicit.length > 0) {
      const decidedByID = new Map(decided.map((idea) => [idea.id, idea]));
      const resolved: ProductIdea[] = [];
      for (const id of explicit) {
        const idea = decidedByID.get(id);
        if (idea && !resolved.includes(idea)) resolved.push(idea);
      }
      return resolved;
    }
    return decided;
  }
  // The narrowed-to-empty save guard evaluates the PROPOSED revision, not
  // the stored one: a changed objective or a terminal reset clears the
  // proposal (no ideas, no scope), so an unrelated new request always saves
  // freely. Only a draft/accepted proposal that keeps decided ideas while
  // resolving to an empty explicit set refuses the silent save — the set
  // serializes with omitempty, so saving it would reload as unspecified
  // and downstream readers would derive (and silently authorize) the
  // remaining decided ideas.
  private scopeNarrowsToEmpty(next: IntentRevision): boolean {
    const stored = this.revision?.effective_outcomes;
    if (!stored || stored.length === 0) return false;
    if (next.effective_outcomes === undefined) return false;
    const decided = (next.ideas || []).some((idea) => idea.decision === 'accept' || idea.decision === 'edit');
    return decided && next.effective_outcomes.length === 0;
  }
  // Decided ideas needing an explicit review after a refused narrowing
  // save: subset members that lost their decision (re-accept to keep the
  // narrowed scope) and decided ideas outside the resolved scope (decline
  // to accept the empty scope explicitly). Rendered alongside the refusal
  // because task-unlinked ideas carry no choice control of their own.
  private scopeGateLists(): {removed: ProductIdea[]; remaining: ProductIdea[]} | undefined {
    if (!this.scopeGateArmed) return undefined;
    const revision = this.revision;
    const stored = revision?.effective_outcomes;
    if (!revision || !stored || stored.length === 0) return undefined;
    const byID = new Map(revision.ideas.map((idea) => [idea.id, idea]));
    const decidedIDs = new Set(
      revision.ideas.filter((idea) => idea.decision === 'accept' || idea.decision === 'edit').map((idea) => idea.id)
    );
    const resolvedIDs = new Set(this.resolvedScope(revision).map((idea) => idea.id));
    return {
      removed: stored.flatMap((id) => {
        const idea = byID.get(id);
        return idea && !decidedIDs.has(id) ? [idea] : [];
      }),
      remaining: revision.ideas.filter((idea) => decidedIDs.has(idea.id) && !resolvedIDs.has(idea.id)),
    };
  }
  private scopeRecovery() {
    const gate = this.scopeGateLists();
    if (!gate || (gate.removed.length === 0 && gate.remaining.length === 0)) return '';
    return html`<div class="scope-gate" role="group" aria-label="Narrowed scope recovery">
      <p>Decide each listed idea explicitly, then save again.</p>
      ${gate.removed.map(
        (idea) => html`<p>
          ${idea.description}
          <button
            aria-label="Re-accept narrowed idea ${idea.id}"
            ?disabled=${this.busy || this.blocked}
            @click=${() => this.recoverScope(idea.id, 'accept')}
          >
            Re-accept
          </button>
        </p>`
      )}
      ${gate.remaining.map(
        (idea) => html`<p>
          ${idea.description}
          <button
            aria-label="Decline remaining idea ${idea.id}"
            ?disabled=${this.busy || this.blocked}
            @click=${() => this.recoverScope(idea.id, 'decline')}
          >
            Decline
          </button>
        </p>`
      )}
    </div>`;
  }
  private recoverScope = (id: string, decision: 'accept' | 'decline') => {
    if (!this.revision || this.busy || this.blocked) return;
    this.decide(id, decision);
  };
  // Declined ideas behind shelved work: the only entries offering recovery.
  // Re-accepted ideas fall out on their own; restored work leaves the shelf
  // on the next save.
  private shelvedDeclinedIdeas(): ProductIdea[] {
    const byID = new Map((this.revision?.ideas || []).map((idea) => [idea.id, idea]));
    const seen = new Set<string>();
    const declined: ProductIdea[] = [];
    for (const task of this.taskShelf.tasks) {
      for (const id of task.idea_ids) {
        const idea = byID.get(id);
        if (idea?.decision === 'decline' && !seen.has(id)) {
          seen.add(id);
          declined.push(idea);
        }
      }
    }
    return declined;
  }
  private declinedWorkRecovery() {
    if (!this.taskShelf.tasks.length) return '';
    const declined = this.shelvedDeclinedIdeas();
    const count = this.taskShelf.tasks.length;
    return html`<div class="declined-work-gate" role="group" aria-label="Declined work recovery">
      <p>
        ${count} proposed work item${count === 1 ? ' was' : 's were'} removed because
        ${declined.length ? 'a linked idea was declined' : 'the linked ideas changed'}; your other work is kept and the
        proposal saved. Re-accept an idea below to restore its work on the next save, use Change something to regenerate
        work from the remaining direction, or leave the ideas declined to keep the work out.
      </p>
      ${declined.map(
        (idea) => html`<p>
          ${idea.description}
          <button
            aria-label="Re-accept declined idea ${idea.id}"
            ?disabled=${this.busy || this.blocked}
            @click=${() => this.recoverDeclined(idea.id)}
          >
            Re-accept
          </button>
        </p>`
      )}
    </div>`;
  }
  private recoverDeclined = (id: string) => {
    if (!this.revision || this.busy || this.blocked) return;
    this.decide(id, 'accept');
  };
  // Declined ideas with no local shelf coverage: the only ones that may need
  // saved-history recovery. Terminal heads carry no proposal, so they never
  // consult history.
  private historyUncoveredIdeas(): ProductIdea[] {
    const revision = this.revision;
    if (!revision || this.terminalRevision()) return [];
    const shelved = new Set<string>();
    for (const task of this.taskShelf.tasks) for (const id of task.idea_ids) shelved.add(id);
    return (revision.ideas || []).filter((idea) => idea.decision === 'decline' && !shelved.has(idea.id));
  }
  // Saved-history lookup for a shelfless resume (another device): one bare
  // GET on the owner-private history endpoint per head. Permission, CAS and
  // transport failures keep the resume intact and degrade to the honest
  // unavailable note; they never block saved work and never retry in a loop.
  private maybeRecoverFromHistory(): void {
    const revision = this.revision;
    if (!revision || !this.api || !this.active || this.blocked) return;
    const uncovered = this.historyUncoveredIdeas();
    if (uncovered.length === 0) {
      this.savedHistory = [];
      this.savedHistoryNote = '';
      this.savedHistorySkipped = 0;
      return;
    }
    if (this.savedHistoryHead === revision.id) return;
    // Claim the head before the read so one resume issues one history GET.
    this.savedHistoryHead = revision.id;
    this.savedHistory = [];
    this.savedHistorySkipped = 0;
    if (!revision.predecessor) {
      this.savedHistoryNote = savedHistoryNoPredecessor;
      this.requestUpdate();
      return;
    }
    this.savedHistoryNote = '';
    const actor = this.actor;
    const draftScope = this.draftScope;
    const headId = revision.id;
    const epoch = this.requestEpoch;
    const intent = revision.intent;
    void (async () => {
      try {
        const chain = intentHistory(
          await this.call('/api/factory/intents/' + intent + '/history', undefined, AbortSignal.timeout(20000))
        );
        if (
          !this.requestLive(epoch) ||
          this.actor !== actor ||
          this.draftScope !== draftScope ||
          this.revision?.id !== headId
        )
          return;
        this.adoptSavedHistory(chain, uncovered);
      } catch {
        if (
          !this.requestLive(epoch) ||
          this.actor !== actor ||
          this.draftScope !== draftScope ||
          this.revision?.id !== headId
        )
          return;
        if (this.historyUncoveredIdeas().length === 0) return;
        this.savedHistory = [];
        this.savedHistoryNote = savedHistoryUnavailable;
        this.savedHistorySkipped = 0;
        this.requestUpdate();
      }
    })();
  }
  // Bounded exact traversal of the saved chain: follow ONLY the predecessor
  // links the current head names, newest first. The read is bound to the
  // freshly loaded head first: the chain must carry the exact head with its
  // recorded predecessor (a CAS-like freshness check), or nothing recovers.
  // Each hop must stay on the same request chain. A recorded task qualifies
  // only when EVERY linked idea still means the same in the current head
  // (identity, description, consequence and effective correction) and no
  // link beyond the recovered idea is declined: replaying a task across
  // changed linked semantics would guess, so it refuses. A revision that
  // merely bears a graph for other ideas never qualifies, and a same-identity
  // idea with changed meaning stops that idea instead of guessing across it.
  // Only the qualifying tasks are staged, with demonstrably compatible
  // edges (live, staged alongside, or remapped onto a unique
  // exact-idea-identical live successor) plus repairs for recorded kept-edge
  // cuts into the recovered set. Anything unprovable is named in the preview
  // instead of silently pruned or invented — never a union, never a guess.
  private adoptSavedHistory(chain: IntentRevision[], uncovered: ProductIdea[]): void {
    const revision = this.revision;
    if (!revision || this.terminalRevision()) return;
    const byID = new Map(chain.map((entry) => [entry.id, entry]));
    // Cross-device stale-head guard (FJ-443): the chain is newest-first, so
    // the loaded head must be the authoritative newest entry. Another device
    // advancing H→H2 before the history GET yields [H2,H,P]: H is still
    // present with its predecessor, but previewing from it would decide on
    // a stale head (the backend CAS would refuse the durable save, yet the
    // preview and user decision would already be built on the wrong head).
    // Refuse with the honest unavailable state; resuming the fresh head
    // (chain[0]) re-offers that head's recovery. No guessed graph.
    const newest = chain.length > 0 ? chain[0] : undefined;
    if (!newest || newest.id !== revision.id || newest.intent !== revision.intent) {
      this.savedHistory = [];
      this.savedHistoryNote = savedHistoryUnavailable;
      this.savedHistorySkipped = 0;
      this.requestUpdate();
      return;
    }
    const headID = revision.id;
    const headEntry = byID.get(headID);
    if (!headEntry || headEntry.intent !== revision.intent || headEntry.predecessor !== revision.predecessor) {
      this.savedHistory = [];
      this.savedHistoryNote = savedHistoryUnavailable;
      this.savedHistorySkipped = 0;
      this.requestUpdate();
      return;
    }
    const nowByID = new Map(revision.ideas.map((idea) => [idea.id, idea]));
    const live = new Set(revision.tasks.map((task) => task.key));
    const liveByIdeas = new Map<string, string[]>();
    for (const task of revision.tasks) {
      const signature = [...task.idea_ids].sort().join('\0');
      const bucket = liveByIdeas.get(signature);
      if (bucket) bucket.push(task.key);
      else liveByIdeas.set(signature, [task.key]);
    }
    const pastByID = (entry: IntentRevision) => new Map(entry.ideas.map((idea) => [idea.id, idea]));
    const recovered: SavedHistoryEntry[] = [];
    const missing = new Set(uncovered.map((idea) => idea.id));
    const stagedKeys = new Set<string>();
    let skipped = 0;
    let cursor = revision.predecessor;
    for (let hops = 0; cursor && missing.size > 0 && hops < savedHistoryHopBound; hops += 1) {
      const entry = byID.get(cursor);
      if (!entry || entry.intent !== revision.intent) break;
      const wasByID = pastByID(entry);
      for (const idea of uncovered) {
        if (!missing.has(idea.id)) continue;
        const then = wasByID.get(idea.id);
        if (!then) continue;
        if (!sameIdeaMeaning(then, idea)) {
          missing.delete(idea.id);
          continue;
        }
        const linked = entry.tasks.filter((task) => task.idea_ids.includes(idea.id) && !live.has(task.key));
        const candidates = linked.filter((task) =>
          task.idea_ids.every((link) => historyLinkOK(link, idea.id, wasByID, nowByID))
        );
        skipped += linked.length - candidates.length;
        if (candidates.length === 0) continue;
        const batchKeys = new Set(candidates.map((task) => task.key));
        const tasks: IntentTask[] = [];
        const cutEdges: SavedHistoryCutEdge[] = [];
        for (const task of candidates) {
          const kept: string[] = [];
          for (const dep of task.depends_on || []) {
            if (live.has(dep) || stagedKeys.has(dep) || batchKeys.has(dep)) {
              // Same-key live edge (FJ-444/FJ-454): the key resolving live does
              // not prove the target still means the same, nor that it still
              // binds the same work. A kept B whose description/correction
              // changed must not carry the recorded A→B edge forward by key
              // identity alone — and neither may a B that kept its key while
              // its idea links changed (historical B linked b, current B
              // links c): canonical drafts permit changed bindings, so the
              // edge restores only onto an EXACT old/current idea-set match,
              // else it stays cut with an explicit warning, never silent.
              if (live.has(dep) && !stagedKeys.has(dep) && !batchKeys.has(dep)) {
                const pastTarget = entry.tasks.find((candidate) => candidate.key === dep);
                if (pastTarget && pastTarget.idea_ids.length > 0) {
                  const liveTarget = revision.tasks.find((candidate) => candidate.key === dep);
                  const lineageOK =
                    liveTarget !== undefined &&
                    liveTarget.idea_ids.length === pastTarget.idea_ids.length &&
                    [...liveTarget.idea_ids].sort().join('\0') === [...pastTarget.idea_ids].sort().join('\0');
                  const targetOK =
                    lineageOK &&
                    pastTarget.idea_ids.every((link) => {
                      const past = wasByID.get(link);
                      const now = nowByID.get(link);
                      return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
                    });
                  if (!targetOK) {
                    cutEdges.push({key: task.key, objective: task.objective, dep, kind: 'task'});
                    continue;
                  }
                }
              }
              kept.push(dep);
              continue;
            }
            // Outgoing remap (FJ-444): a recorded A→B-old edge remaps onto
            // the live successor only when the target's semantics still
            // match — same idea set AND every linked idea still means the
            // same (description/consequence/correction). Matching by idea
            // IDs alone would guess across a changed B.
            const pastTarget = entry.tasks.find((candidate) => candidate.key === dep);
            const signature = pastTarget ? [...pastTarget.idea_ids].sort().join('\0') : '';
            const successors = signature ? liveByIdeas.get(signature) || [] : [];
            if (pastTarget && successors.length === 1) {
              const successorKey = successors[0] as string;
              const liveTarget = revision.tasks.find((candidate) => candidate.key === successorKey);
              const successorOK =
                pastTarget.idea_ids.length > 0 &&
                liveTarget !== undefined &&
                [...liveTarget.idea_ids].sort().join('\0') === signature &&
                pastTarget.idea_ids.every((link) => {
                  const past = wasByID.get(link);
                  const now = nowByID.get(link);
                  return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
                });
              if (successorOK) {
                kept.push(successorKey);
                continue;
              }
            }
            cutEdges.push({key: task.key, objective: task.objective, dep, kind: 'task'});
          }
          stagedKeys.add(task.key);
          // A remap can preserve the edge count while changing its targets,
          // so compare values — never the length alone — before reusing the
          // recorded shape.
          const unchanged =
            kept.length === (task.depends_on || []).length &&
            kept.every((dep, index) => dep === (task.depends_on || [])[index]);
          tasks.push(unchanged ? {...task} : {...task, depends_on: kept});
        }
        // Repairs for the other direction (kept-depends-on-removed): a
        // recorded edge from a kept (unstaged) task into the recovered set
        // returns only onto a demonstrably compatible owner — live under the
        // recorded key or the unique exact-idea-identical live successor,
        // with every owner link still meaning the same. Anything else waits
        // unassigned (a later regeneration may claim it through the ordinary
        // shelf machinery) and the preview warns instead of guessing.
        const repairs: SavedHistoryRepair[] = [];
        for (const owner of entry.tasks) {
          if (batchKeys.has(owner.key) || stagedKeys.has(owner.key)) continue;
          const incoming = (owner.depends_on || []).filter((dep) => batchKeys.has(dep) || stagedKeys.has(dep));
          if (incoming.length === 0) continue;
          const ownerOK = owner.idea_ids.every((link) => {
            const past = wasByID.get(link);
            const now = nowByID.get(link);
            return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
          });
          // Narrowed recovery (FJ-444): stage ONLY justified cut edges into
          // the recovered set — an incoming edge whose target still means
          // the same (every linked idea identical past vs now). The full
          // historical B.depends_on (e.g., old B→[A,C] recovering A while
          // current B deliberately dropped C) must never resurrect the
          // deliberately removed C, although the preview promises only A.
          // Unjustified incoming edges warn instead of staging.
          const justified = incoming.filter((dep) => {
            const pastTarget = entry.tasks.find((candidate) => candidate.key === dep);
            if (!pastTarget || pastTarget.idea_ids.length === 0) return false;
            return pastTarget.idea_ids.every((link) => {
              const past = wasByID.get(link);
              const now = nowByID.get(link);
              return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
            });
          });
          const depIdeas: Record<string, string[]> = {};
          for (const dep of justified) {
            const pastTarget = entry.tasks.find((candidate) => candidate.key === dep);
            if (pastTarget && pastTarget.idea_ids.length > 0) depIdeas[dep] = [...pastTarget.idea_ids];
          }
          const orderedDeps = (owner.depends_on || []).filter((dep) => justified.includes(dep));
          const repair = {
            deps: orderedDeps,
            ideas: [...owner.idea_ids],
            depIdeas,
            targets: [...justified],
          };
          if (!ownerOK) {
            for (const dep of incoming)
              cutEdges.push({key: owner.key, objective: owner.objective, dep, kind: 'repair'});
            continue;
          }
          for (const dep of incoming) {
            if (!justified.includes(dep))
              cutEdges.push({key: owner.key, objective: owner.objective, dep, kind: 'repair'});
          }
          if (justified.length === 0) continue;
          const signature = [...owner.idea_ids].sort().join('\0');
          const successors = liveByIdeas.get(signature) || [];
          // Same-key live owner (FJ-454): the key resolving live does not
          // prove the work still binds the same ideas. A same-key owner
          // whose idea set changed (historical B linked b, current B links
          // c) must never resolve silently — canonical drafts permit changed
          // bindings, so the recorded edge stays cut with an explicit
          // warning instead of restoring onto changed work. Only an EXACT
          // old/current idea-set match resolves by key.
          const liveOwner = live.has(owner.key)
            ? revision.tasks.find((candidate) => candidate.key === owner.key)
            : undefined;
          const liveOwnerSameIdeas =
            liveOwner !== undefined &&
            liveOwner.idea_ids.length === owner.idea_ids.length &&
            [...liveOwner.idea_ids].sort().join('\0') === signature;
          if (live.has(owner.key) && !liveOwnerSameIdeas) {
            for (const dep of incoming)
              cutEdges.push({key: owner.key, objective: owner.objective, dep, kind: 'repair'});
            continue;
          }
          if (live.has(owner.key)) repairs.push({...repair, owner: owner.key, resolved: true});
          else if (successors.length === 1) repairs.push({...repair, owner: successors[0] as string, resolved: true});
          else repairs.push({...repair, owner: owner.key, resolved: false});
        }
        recovered.push({
          idea: idea.id,
          revision: entry.id,
          tasks,
          repairs,
          cutEdges,
          ideas: entry.ideas.map((row) => ({...row})),
        });
        missing.delete(idea.id);
      }
      cursor = entry.predecessor;
    }
    this.savedHistory = recovered;
    this.savedHistorySkipped = skipped;
    this.savedHistoryNote =
      recovered.length === 0 ? savedHistoryUnavailable : missing.size > 0 ? savedHistoryPartial : '';
    this.requestUpdate();
  }
  // User-driven re-accept from saved history: stage ONLY the recorded bytes
  // for this idea into the shelf, then decide through the ordinary path so
  // the next save drafts them onto the freshly checked current head. Rows
  // are bound to the head they were derived from: a stale row refuses and
  // cleans up instead of acting. Decisions may have moved since the lookup,
  // so every task and repair is re-verified against the fresh head before
  // it stages — changed links refuse, proven edges and repairs merge into
  // the shelf (never overwriting live work or local records), and the
  // existing save-time rules still govern the final draft. No edge is ever
  // invented.
  private recoverFromHistory = (id: string) => {
    const revision = this.revision;
    if (!revision || this.busy || this.blocked) return;
    if (this.savedHistoryHead !== revision.id) {
      this.savedHistory = [];
      this.savedHistoryNote = '';
      this.savedHistorySkipped = 0;
      return;
    }
    const slot = this.savedHistory.findIndex((row) => row.idea === id);
    const entry = slot >= 0 ? this.savedHistory[slot] : undefined;
    const idea = revision.ideas.find((row) => row.id === id);
    if (!entry || !idea || idea.decision !== 'decline') return;
    const nowByID = new Map(revision.ideas.map((row) => [row.id, row]));
    const wasByID = new Map(entry.ideas.map((row) => [row.id, row]));
    const live = new Set(revision.tasks.map((task) => task.key));
    const proposed = new Set(revision.ideas.map((row) => row.id));
    const tasks = entry.tasks.filter(
      (task) =>
        task.idea_ids.includes(id) &&
        !live.has(task.key) &&
        task.idea_ids.length > 0 &&
        task.idea_ids.every((link) => proposed.has(link)) &&
        task.idea_ids.every((link) => historyLinkOK(link, id, wasByID, nowByID))
    );
    if (tasks.length === 0) return;
    const staged = new Set(tasks.map((task) => task.key));
    for (const task of tasks) {
      if (this.taskShelf.tasks.some((shelved) => shelved.key === task.key)) continue;
      // Staged task edges (FJ-444): a same-key live dep still requires target
      // semantics — drop it when the live target's linked ideas changed
      // meaning, never carry it by key identity alone.
      const deps = (task.depends_on || []).filter((dep) => {
        if (staged.has(dep)) return true;
        if (!live.has(dep)) return false;
        const liveTarget = revision.tasks.find((candidate) => candidate.key === dep);
        if (!liveTarget || liveTarget.idea_ids.length === 0) return true;
        return liveTarget.idea_ids.every((link) => {
          const past = wasByID.get(link);
          const now = nowByID.get(link);
          return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
        });
      });
      this.taskShelf.addRecovered(
        deps.length === (task.depends_on || []).length ? {...task} : {...task, depends_on: deps}
      );
    }
    for (const repair of entry.repairs) {
      // Narrowed staging (FJ-444): merge ONLY justified edges into the
      // recovered set — deps pointing at staged work whose recorded target
      // lineage still means the same. A live-only dep (e.g., deliberately
      // removed C that is still live) must never merge, even though the
      // preview promises only A. Preserve current other edges by merging
      // the filtered subset, never the full historical list.
      const justifiedDeps = repair.deps.filter((dep) => {
        if (!staged.has(dep)) return false;
        const ids = repair.depIdeas[dep];
        if (!ids || ids.length === 0) return true;
        return ids.every((link) => {
          const past = wasByID.get(link);
          const now = nowByID.get(link);
          return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
        });
      });
      if (justifiedDeps.length === 0) continue;
      const ownerOK =
        repair.ideas.length > 0 &&
        repair.ideas.every((link) => {
          const past = wasByID.get(link);
          const now = nowByID.get(link);
          return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
        });
      if (!ownerOK) continue;
      const narrowedDepIdeas: Record<string, string[]> = {};
      for (const dep of justifiedDeps) {
        const ids = repair.depIdeas[dep];
        if (ids) narrowedDepIdeas[dep] = [...ids];
      }
      this.taskShelf.mergeRepair(repair.owner, {
        ideas: repair.ideas,
        deps: [...justifiedDeps],
        depIdeas: narrowedDepIdeas,
      });
    }
    this.savedHistory = this.savedHistory.filter((row) => row.idea !== id);
    if (this.historyUncoveredIdeas().length === 0) {
      this.savedHistoryNote = '';
      this.savedHistorySkipped = 0;
    }
    this.decide(id, 'accept');
  };
  // Saved-history recovery gate: the explicit preview (source revision, work,
  // proven edges and recorded repairs per idea) plus the honest unavailable
  // states. Rows render only for the head they were derived from; stale
  // rows never stay actionable. Unsent edits stay local-only: only saved
  // bytes are ever shown here.
  private savedHistoryRecovery() {
    const revision = this.revision;
    if (!revision || this.terminalRevision()) return '';
    if (this.savedHistoryHead !== revision.id) return '';
    const uncovered = new Set(this.historyUncoveredIdeas().map((idea) => idea.id));
    if (uncovered.size === 0) return '';
    const byID = new Map(revision.ideas.map((idea) => [idea.id, idea]));
    const live = new Set(revision.tasks.map((task) => task.key));
    const rows: {idea: ProductIdea; entry: SavedHistoryEntry; tasks: IntentTask[]; repairs: SavedHistoryRepair[]}[] =
      [];
    for (const entry of this.savedHistory) {
      if (!uncovered.has(entry.idea)) continue;
      const idea = byID.get(entry.idea);
      if (!idea || idea.decision !== 'decline') continue;
      const wasByID = new Map(entry.ideas.map((row) => [row.id, row]));
      const tasks = entry.tasks.filter(
        (task) =>
          task.idea_ids.includes(entry.idea) &&
          !live.has(task.key) &&
          task.idea_ids.every((link) => historyLinkOK(link, entry.idea, wasByID, byID))
      );
      if (tasks.length === 0) continue;
      const staged = new Set(tasks.map((task) => task.key));
      // Preview narrows like staging (FJ-444): only repairs with a
      // justified edge into the staged set render as returning; live-only
      // deps (deliberately removed edges) never promise.
      const repairs = entry.repairs.filter((repair) => {
        if (!live.has(repair.owner)) return false;
        const justified = repair.deps.filter((dep) => {
          if (!staged.has(dep)) return false;
          const ids = repair.depIdeas[dep];
          if (!ids || ids.length === 0) return true;
          return ids.every((link) => {
            const past = wasByID.get(link);
            const now = byID.get(link);
            return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
          });
        });
        if (justified.length === 0) return false;
        return repair.ideas.every((link) => {
          const past = wasByID.get(link);
          const now = byID.get(link);
          return past !== undefined && now !== undefined && sameIdeaMeaning(past, now);
        });
      });
      rows.push({idea, entry, tasks, repairs});
    }
    const pending = this.savedHistory.filter(
      (entry) =>
        uncovered.has(entry.idea) &&
        byID.get(entry.idea)?.decision === 'decline' &&
        entry.repairs.some((repair) => !live.has(repair.owner))
    ).length;
    if (rows.length === 0) {
      if (!this.savedHistoryNote) return '';
      return html`<div class="declined-work-gate" role="group" aria-label="Saved history recovery">
        <p>${this.savedHistoryNote}</p>
      </div>`;
    }
    return html`<div class="declined-work-gate" role="group" aria-label="Saved history recovery">
      <p>
        Saved history holds work for re-accept consideration. Only saved revisions are shown; unsent edits stay on this
        device, and work that was never saved cannot be recovered.
      </p>
      ${rows.map(({idea, entry, tasks, repairs}) => {
        const returning = repairs.filter((repair) => repair.targets.some((dep) => !live.has(dep)));
        return html`<p>
          Saved revision ${entry.revision} records ${tasks.length} work item${tasks.length === 1 ? '' : 's'} for
          “${idea.description}”:
          ${tasks
            .map(
              (task) =>
                `${task.objective}${task.depends_on?.length ? ` (depends on ${task.depends_on.join(', ')})` : ''}`
            )
            .join('; ')}
          ${
            returning.length > 0
              ? html`Recorded ${returning.length === 1 ? 'dependency' : 'dependencies'}
                ${returning.map((repair) => repair.targets.join(', ')).join('; ')}
                return${returning.length === 1 ? 's' : ''} on re-accept.`
              : ''
          }
          ${entry.cutEdges.map(
            (cut) =>
              html`Recorded edge ${cut.objective} → ${cut.dep} stays cut instead of being guessed. No edge was invented.`
          )}
          <button
            aria-label="Re-accept declined idea from saved history ${entry.idea}"
            ?disabled=${this.busy || this.blocked}
            @click=${() => this.recoverFromHistory(entry.idea)}
          >
            Re-accept
          </button>
        </p>`;
      })}
      ${
        pending > 0
          ? html`<p>
              ${pending} recorded dependenc${pending === 1 ? 'y has' : 'ies have'} no unique live successor yet, so
              ${pending === 1 ? 'it waits' : 'they wait'} unassigned instead of being guessed.
            </p>`
          : ''
      }
      ${
        this.savedHistorySkipped > 0
          ? html`<p>
              ${this.savedHistorySkipped} other recorded work item${this.savedHistorySkipped === 1 ? '' : 's'} were left
              out: their linked ideas changed or are declined in the current head. Nothing was guessed.
            </p>`
          : ''
      }
      ${this.savedHistoryNote ? html`<p>${this.savedHistoryNote}</p>` : ''}
    </div>`;
  }
  // Unresolved repair review: a recorded dependency whose work has no
  // unique successor yet stays visible — across saves, reloads and leaves
  // — instead of being silently erased. No edge is invented while it waits:
  // a later regeneration that produces a unique successor re-attaches it
  // automatically (Change something can force that regeneration), and the
  // per-record Confirm drop is the explicit user resolution when no
  // successor will ever come. Dropping removes the recovery record only;
  // the saved graph is untouched.
  private unresolvedRepairReview() {
    const pending = this.taskShelf.unresolved(this.revision);
    if (pending.length === 0) return '';
    const byID = new Map((this.revision?.ideas || []).map((idea) => [idea.id, idea]));
    return html`<div class="unresolved-repair-gate" role="group" aria-label="Unresolved dependency repair">
      <p>
        A recorded dependency has no unique successor to re-attach to, so it is kept unassigned rather than guessed. No
        dependency was invented. It resolves on its own when its work reappears uniquely (Change something can
        regenerate the work), or confirm below that the recorded repair should be dropped.
      </p>
      ${pending.map(
        ({key, repair}) => html`<p>
          ${repair.ideas.map((id) => byID.get(id)?.description || id).join('; ')}
          <button
            aria-label="Confirm drop of unassigned repair ${key}"
            ?disabled=${this.busy || this.blocked}
            @click=${() => this.dropUnresolvedRepair(key)}
          >
            Confirm drop
          </button>
        </p>`
      )}
    </div>`;
  }
  private dropUnresolvedRepair = (key: string) => {
    if (!this.revision || this.busy || this.blocked) return;
    if (!this.taskShelf.unresolved(this.revision).some((entry) => entry.key === key)) return;
    this.taskShelf.dropRepair(key);
    this.rememberDraft();
    this.requestUpdate();
  };
  private reconcileShelvedTasks(proposal: IntentRevision) {
    if (this.taskShelf.reconcile(proposal))
      this.error =
        'Exploration split linked work into several items with identical idea links, so a recorded dependency repair was left unassigned instead of guessed. Review the proposed work and use Change something to correct it; no dependency was invented.';
    this.requestUpdate();
  }
  private async save(state: IntentRevision['state'], repository: string, signal: AbortSignal) {
    if (this.explorationBlocked()) throw Error('Exploration command needs recovery');
    if (this.planningBlocked()) throw Error('Planning command needs recovery');
    const next = this.nextRevision(state, repository);
    if ((state === 'draft' || state === 'accepted') && this.scopeNarrowsToEmpty(next)) {
      this.error =
        'Your latest choice emptied the explicitly narrowed scope while decided ideas remain. Review the authorized ideas explicitly before saving; nothing was saved and no remaining idea was authorized.';
      this.scopeGateArmed = true;
      throw Error('Explicit scope narrowed to empty with decided ideas remaining');
    }
    this.scopeGateArmed = false;
    const address = this.draftAddress();
    const pendingRead = readSave(address);
    if (pendingRead.blocked || pendingRead.pending) {
      this.saveNotice =
        'An earlier save needs confirmation. Check its saved result before saving again. Current writing is kept.';
      throw Error('Unresolved saved request');
    }
    const before = this.captureDraft();
    const after: RequestDraft = {
      ...this.captureDraft(),
      ...this.taskShelf.afterSave(next, this.revision),
      base: next.id,
      repository: next.repository,
      ideas: next.ideas.map(({id, decision, correction}) => ({
        id,
        decision,
        ...(correction === undefined ? {} : {correction}),
      })),
      ...(next.approach === undefined ? {} : {approach: next.approach}),
    };
    const host = await this.saveIdentity(signal);
    const pending: PendingSave = {host, revision: next, before, after};
    if (!(await retainSave(address, pending))) {
      this.saveNotice =
        'The exact save could not be retained durably, or another save is pending. No request was sent. Current writing is kept.';
      throw Error('Save persistence unavailable');
    }
    signal.throwIfAborted();
    this.saveNotice = 'This save is awaiting confirmation. Check its saved result if the reply is lost.';
    this.saveRetry = false;
    this.saveObsolete = false;
    const saved = savedReceipt(
      await this.call('/api/factory/intents', {...pending.revision}, signal),
      address.actor,
      pending
    );
    signal.throwIfAborted();
    if (!(await this.confirmSave(address, pending, saved, true, signal)))
      throw Error('Save confirmed without replacing later writing');
    return saved;
  }
  private async saveIdentity(signal: AbortSignal): Promise<string> {
    const row = object(await this.call('/api/factory/identity', undefined, signal));
    check(row.actor === this.actor && typeof row.host === 'string' && /^[a-f0-9]{32}$/.test(row.host));
    return row.host;
  }
  private async confirmSave(
    address: RequestDraftAddress,
    pending: PendingSave,
    saved: IntentRevision,
    currentHead: boolean,
    signal: AbortSignal
  ) {
    if (!(await settleSave(address, pending))) {
      signal.throwIfAborted();
      this.saveNotice =
        'The saved result was confirmed, but its recovery metadata could not be retained. Current writing and the pending save are kept; check again after storage is available.';
      return false;
    }
    signal.throwIfAborted();
    this.saveRetry = false;
    const writing = JSON.stringify(this.captureDraft());
    const sameWriting =
      writing === JSON.stringify(pending.before) ||
      (this.revision?.id === saved.id &&
        writing === JSON.stringify({...pending.before, base: saved.id, repository: saved.repository}));
    if (!sameWriting || !currentHead) {
      this.saveNotice =
        'The original save was confirmed. Later writing or a newer saved revision was kept; no planning or generation was started.';
      return false;
    }
    this.taskShelf.reset(pending.after);
    this.revision = saved;
    this.savedSnapshot = structuredClone(saved);
    this.dirty = false;
    this.rememberDraft();
    this.savedHistory = [];
    this.savedHistoryNote = '';
    this.savedHistorySkipped = 0;
    this.savedHistoryHead = '';
    this.maybeRecoverFromHistory();
    this.saveNotice = '';
    return true;
  }
  private recoverSave = (retry = false, release = false) => {
    void this.perform(
      async (signal) => {
        const address = this.draftAddress(),
          read = readSave(address),
          pending = read.pending;
        if (read.blocked || !pending) throw Error('Save record unavailable');
        const host = await this.saveIdentity(signal);
        check(host === pending.host);
        // Re-read after asynchronous admission: unknown or replacement records stay untouched.
        const current = readSave(address);
        check(current.pending && JSON.stringify(current.pending) === JSON.stringify(pending));
        let response: unknown;
        try {
          response = retry
            ? await this.call('/api/factory/intents', {...pending.revision}, signal)
            : await this.call(
                '/api/factory/intents/' + pending.revision.intent + '/revisions/' + pending.revision.id,
                undefined,
                signal
              );
        } catch (error) {
          signal.throwIfAborted();
          if (!retry && error instanceof SodaRequestError && error.status === 404) {
            this.saveObsolete = false;
            try {
              const head = intentRevision(
                await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
              );
              check(head.intent === pending.revision.intent && decisionID(head.id));
              this.saveObsolete = head.id !== pending.revision.id && head.id !== pending.revision.predecessor;
            } catch (headError) {
              signal.throwIfAborted();
              if (!(headError instanceof SodaRequestError && headError.status === 404)) throw headError;
            }
            if (this.saveObsolete) {
              // Read absence after observing the advanced head: a save could have
              // committed between the first lookup and that head read.
              try {
                const receipt = savedReceipt(
                  await this.call(
                    '/api/factory/intents/' + pending.revision.intent + '/revisions/' + pending.revision.id,
                    undefined,
                    signal
                  ),
                  address.actor,
                  pending
                );
                await this.confirmSave(address, pending, receipt, false, signal);
                return;
              } catch (receiptError) {
                signal.throwIfAborted();
                if (!(receiptError instanceof SodaRequestError && receiptError.status === 404)) throw receiptError;
              }
              this.saveRetry = false;
              if (release) {
                check(await releaseObsoleteSave(address, pending));
                signal.throwIfAborted();
                this.saveObsolete = false;
                this.saveNotice =
                  'The obsolete save was released. Current writing was kept; open the current saved request before saving a successor.';
              } else
                this.saveNotice =
                  'The original save is absent and its expected predecessor has been replaced. It cannot replace the saved request. You can release this obsolete save while keeping current writing.';
              return;
            }
            this.saveRetry = true;
            this.saveNotice =
              'The original save is not recorded yet. It may still finish. Check again, or explicitly retry that same save identity; current writing stays kept.';
            return;
          }
          throw error;
        }
        const saved = savedReceipt(response, address.actor, pending);
        const head = intentRevision(await this.call('/api/factory/intents/' + saved.intent, undefined, signal));
        check(head.intent === saved.intent && decisionID(head.id));
        const adopted = await this.confirmSave(address, pending, saved, head.id === saved.id, signal);
        if (adopted) {
          this.saveNotice =
            'The original save was confirmed. Continue explicitly when ready; no planning or generation was started.';
          this.dispatchEvent(
            new CustomEvent('intent-saved', {
              detail: {intent: saved.intent, revision: saved.id, state: saved.state},
              bubbles: true,
              composed: true,
            })
          );
        }
      },
      retry ? 'Retrying the original save…' : 'Checking the saved request…'
    );
  };
  private saveRecovery() {
    if (!this.actorValid()) return '';
    const read = readSave(this.draftAddress());
    if (!read.pending && !read.blocked && !this.saveNotice) return '';
    return html`<section aria-label="Request save recovery">
      <p role="status">
        ${read.blocked ? 'Saved command ownership or contents could not be verified. Its bytes were preserved; no new save can be sent.' : this.saveNotice || 'An earlier save needs confirmation. Current writing is kept; no command was replayed.'}
      </p>
      ${
        read.pending
          ? html`<label
                >Original saved outcome<textarea readonly .value=${read.pending.revision.objective}></textarea>
              </label>
              <button
                type="button"
                ?disabled=${this.busy || this.blocked || !this.active}
                @click=${() => this.recoverSave()}
              >
                Check saved result
              </button>
              ${this.saveRetry ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverSave(true)}>Retry original save</button>` : ''}
              ${this.saveObsolete ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverSave(false, true)}>Release obsolete save</button>` : ''}`
          : ''
      }
    </section>`;
  }

  private explorationBlocked(): boolean {
    const address = this.generationAddress();
    if (!address) return false;
    const read = readExplorationCommand(address);
    if (!read.blocked && !read.pending) return false;
    this.explorationCommandNotice =
      'An earlier exploration needs confirmation. Current writing is kept; check its original result before saving or continuing.';
    return true;
  }
  private explorationPath(revision: IntentRevision) {
    return '/api/factory/intents/' + revision.intent + '/revisions/' + revision.id + '/exploration';
  }
  private async explore(signal: AbortSignal) {
    if (this.explorationBlocked()) return;
    const saved = await this.save('draft', this.revision?.repository || '0', signal);
    signal.throwIfAborted();
    try {
      const address = this.generationAddress();
      check(address);
      const pending: PendingExploration = {
        host: await this.saveIdentity(signal),
        revision: structuredClone(saved),
        writing: this.captureDraft(),
      };
      if (!(await retainExploration(address, pending))) {
        this.explorationCommandNotice =
          'The exact exploration request could not be saved on this device, or another request is pending. No exploration was sent; current writing is kept.';
        return;
      }
      signal.throwIfAborted();
      this.explorationCanResume = this.explorationCanRelease = false;
      this.explorationCommandNotice =
        'The original exploration is awaiting confirmation. Check its saved result if the reply is lost.';
      const result = await this.postExploration(address, pending, signal);
      await this.confirmExploration(address, pending, result, true, signal);
    } catch (error) {
      if (!signal.aborted) this.exploreFailed(error);
    }
  }
  private explorationRefused(error: unknown): error is SodaRequestError {
    return (
      error instanceof SodaRequestError &&
      ((error.status === 400 && ['invalid_proposal_task_limit', 'invalid_proposal'].includes(error.code || '')) ||
        (error.status === 503 && error.code === 'planning_unavailable') ||
        (error.status === 409 && error.code === 'planning_funding_required'))
    );
  }
  private async postExploration(address: RequestDraftAddress, pending: PendingExploration, signal: AbortSignal) {
    let response: unknown;
    try {
      response = await this.call(
        '/api/factory/intents/' + pending.revision.intent + '/explore',
        {revision: pending.revision.id},
        signal
      );
    } catch (error) {
      if (this.explorationRefused(error)) {
        if (await settleExploration(address, pending)) {
          signal.throwIfAborted();
          this.explorationCommandNotice =
            'The original exploration was refused. Its writing was kept; no proposal was confirmed.';
        }
      }
      throw error;
    }
    return explorationResult(response, pending, address.actor);
  }
  private async confirmExploration(
    address: RequestDraftAddress,
    pending: PendingExploration,
    result: ExplorationResult,
    currentHead: boolean,
    signal: AbortSignal
  ) {
    if (!(await settleExploration(address, pending, result))) {
      signal.throwIfAborted();
      this.explorationCommandNotice =
        'The original exploration result is recorded, but its writing or pending identity could not be saved. Both copies are kept; check again after storage is available.';
      return;
    }
    signal.throwIfAborted();
    this.explorationCanResume = this.explorationCanRelease = false;
    this.explorationCommandNotice =
      'The original exploration is confirmed. Current writing is kept; no planning, acceptance or work was started.';
    if (
      !currentHead ||
      this.revision?.id !== pending.revision.id ||
      JSON.stringify(this.captureDraft()) !== JSON.stringify(pending.writing)
    )
      return;
    const proposal = result.revision;
    this.exploration = result.advice;
    this.restoredReview = {basis: pending.revision.id, result: proposal.id};
    this.revision = proposal;
    this.reconcileShelvedTasks(proposal);
    this.savedSnapshot = structuredClone(proposal);
    this.changeOpen = false;
    this.changeText = '';
    this.dirty = false;
    this.rememberDraft();
    this.savedHistory = [];
    this.savedHistoryNote = '';
    this.savedHistorySkipped = 0;
    this.savedHistoryHead = '';
    this.maybeRecoverFromHistory();
  }
  private recoverExploration = (resume = false, release = false) => {
    void this.perform(
      async (signal) => {
        try {
          const address = this.generationAddress();
          check(address);
          const read = readExplorationCommand(address),
            pending = read.pending;
          check(!read.blocked && pending);
          check((await this.saveIdentity(signal)) === pending.host);
          const retained = readExplorationCommand(address);
          check(!retained.blocked && retained.pending && JSON.stringify(retained.pending) === JSON.stringify(pending));
          let head: IntentRevision | undefined;
          if (release) {
            head = intentRevision(
              await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
            );
            check(head.intent === pending.revision.intent && decisionID(head.id));
          }
          if (resume) {
            const posted = await this.postExploration(address, pending, signal);
            head = intentRevision(
              await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
            );
            check(head.intent === pending.revision.intent && decisionID(head.id));
            await this.confirmExploration(address, pending, posted, head.id === posted.revision.id, signal);
            return;
          }
          const result = explorationReceipt(
            await this.call(this.explorationPath(pending.revision), undefined, signal),
            pending,
            address.actor
          );
          if (!head) {
            head = intentRevision(
              await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
            );
            check(head.intent === pending.revision.intent && decisionID(head.id));
          }
          if (result) {
            await this.confirmExploration(address, pending, result, head.id === result.revision.id, signal);
            return;
          }
          this.explorationCanResume = head.id === pending.revision.id;
          this.explorationCanRelease = !this.explorationCanResume;
          if (release && this.explorationCanRelease) {
            check(await settleExploration(address, pending));
            signal.throwIfAborted();
            this.explorationCanRelease = false;
            this.explorationCommandNotice =
              'No exploration result was recorded for the superseded input. Its pending request was released; current writing is kept.';
          } else
            this.explorationCommandNotice =
              'The original exploration is not recorded yet. Consultation may still finish and may have consumed credit. Current writing and the exact request are kept.';
        } catch (error) {
          signal.throwIfAborted();
          this.explorationCanResume = this.explorationCanRelease = false;
          const address = this.generationAddress(),
            read = address && readExplorationCommand(address);
          if (this.explorationRefused(error) && read && !read.blocked && !read.pending) {
            this.exploreFailed(error);
            return;
          }
          this.explorationCommandNotice =
            'The original exploration result could not be verified. Current writing and its exact saved request are kept; check again when access is available.';
        }
      },
      resume ? 'Resuming the original exploration…' : 'Checking the original exploration…'
    );
  };
  private explorationRecovery() {
    const address = this.generationAddress();
    if (!address) return '';
    const read = readExplorationCommand(address);
    if (!read.pending && !read.blocked && !this.explorationCommandNotice) return '';
    return html`<section aria-label="Exploration recovery">
      <p role="status">
        ${read.blocked ? 'Saved exploration ownership or contents could not be verified. Its bytes were preserved; no new exploration can be sent.' : this.explorationCommandNotice || 'An earlier exploration needs confirmation. No command was replayed.'}
      </p>
      ${
        read.pending
          ? html`<label
                >Original exploration outcome<textarea readonly .value=${read.pending.revision.objective}></textarea>
              </label>
              ${read.pending.writing.changeText ? html`<label>Original exploration notes<textarea readonly .value=${read.pending.writing.changeText}></textarea></label>` : ''}
              <button
                type="button"
                ?disabled=${this.busy || this.blocked || !this.active}
                @click=${() => this.recoverExploration()}
              >
                Check exploration result
              </button>
              ${this.explorationCanResume ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverExploration(true)}>Resume original exploration</button>` : ''}
              ${this.explorationCanRelease ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverExploration(false, true)}>Release superseded exploration</button>` : ''}`
          : ''
      }
    </section>`;
  }
  private exploreFailed(error: unknown) {
    if (error instanceof SodaRequestError && error.code === 'planning_unavailable')
      this.error =
        'The TypeSafe System One judgment service could not complete exploration. Your description is saved; try again.';
    else if (error instanceof SodaRequestError && error.code === 'invalid_proposal_task_limit')
      this.error =
        'The derived work exceeds your selected issue limit. Your description is saved; review the planning limit or narrow the approach, then explore again. Limits never increase on their own.';
    else if (error instanceof SodaRequestError && error.code === 'invalid_proposal')
      this.error =
        'The derived proposal failed review validation. Your description is saved; review the approach and explore again.';
    else if (error instanceof SodaRequestError && error.code === 'planning_funding_required')
      this.error =
        'Exploration needs planning credit. Your description is saved; check its allowance before trying again.';
    else
      this.error =
        'Exploration could not be confirmed. Your description is saved; check the original exploration result before trying again.';
  }
  private describe = () => {
    if (this.objective.trim()) void this.perform((signal) => this.explore(signal));
  };
  private saveCurrent = async (signal: AbortSignal) => {
    const saved = await this.save('draft', this.revision?.repository || '0', signal);
    signal.throwIfAborted();
    this.dirty = false;
    this.dispatchEvent(
      new CustomEvent('intent-saved', {
        detail: {intent: saved.intent, revision: saved.id, state: saved.state},
        bubbles: true,
        composed: true,
      })
    );
  };
  private revise = () => {
    const saved = this.savedSnapshot;
    if (
      this.dirty &&
      saved?.state === 'draft' &&
      this.revision?.id === saved.id &&
      this.objective.trim() === saved.objective &&
      this.department === (saved.department || '') &&
      this.revision.approach === saved.approach &&
      JSON.stringify(this.revision.ideas) === JSON.stringify(saved.ideas)
    ) {
      // A limit edit preserves the reviewed product scope and its task keys.
      // The new draft still needs its own native plan and explicit acceptance.
      void this.perform(this.saveCurrent, 'Saving planning limits…');
      return;
    }
    void this.perform((signal) => this.explore(signal));
  };
  private decline = () => {
    void this.perform(async (signal) => {
      await this.save('declined', '0', signal);
      signal.throwIfAborted();
      this.scopeGateArmed = false;
      this.taskShelf.reset();
      this.savedHistory = [];
      this.savedHistoryNote = '';
      this.savedHistorySkipped = 0;
      this.savedHistoryHead = '';
      this.rememberDraft();
      this.revision = undefined;
      this.savedSnapshot = undefined;
      this.exploration = undefined;
      this.restoredReview = undefined;
      this.generation = undefined;
      this.generationError = '';
      this.generationIntent = '';
      this.plan = undefined;
      this.planNotice = '';
      this.planIntent = '';
      this.dirty = false;
      this.changeOpen = false;
      this.changeText = '';
    });
  };
  async acceptRepository(repository: string): Promise<boolean> {
    if (this.explorationBlocked() || this.planningBlocked() || !reviewReady(this.view())) return false;
    return this.perform(async (signal) => {
      const current = this.revision;
      if (!current) throw Error('Product direction unavailable');
      if (current.state !== 'accepted' || current.repository !== repository) {
        if (current.repository !== repository) {
          const bound = await this.save('draft', repository, signal);
          signal.throwIfAborted();
          this.dirty = false;
          this.dispatchEvent(
            new CustomEvent('intent-saved', {
              detail: {intent: bound.intent, revision: bound.id, state: bound.state},
              bubbles: true,
              composed: true,
            })
          );
          await this.startPlanning(bound, signal);
          return;
        }
        const planned = intentPlanStatus(
          await this.call('/api/factory/intents/' + current.intent + '/plan', undefined, signal)
        );
        signal.throwIfAborted();
        if (planned.revision !== current.id) throw Error('Planning status answers another draft');
        this.plan = planned;
        if (planned.state !== 'completed') {
          this.plan = planned;
          if (['none', 'failed', 'cancelled'].includes(planned.state) || plannerStopped(planned))
            await this.planLiveAttempt(current, signal);
          else this.generationNotice = 'Your request is saved. Its planning progress and any question appear here.';
          return;
        }
        if (!planned.walkthroughs) {
          this.plan = planned;
          this.planNotice =
            'This saved plan has no request-specific walkthrough. Author one in a new plan before accepting.';
          return;
        }
        if (planned.walkthroughs && !planned.review) {
          const review = parseExploration(
            await this.call('/api/factory/intents/' + current.intent + '/plan/review', {revision: current.id}, signal)
          );
          signal.throwIfAborted();
          check(
            review.content.revision === current.id &&
              review.recommendation?.revision === current.id &&
              review.consistency?.revision === current.id
          );
          this.plan = {...planned, review};
          const findings = review.consistency?.mismatches.length || 0;
          this.planNotice = findings
            ? 'The authored walkthrough needs a correction. Correct or clarify the request above (your objective and choices stay with your authorship), then plan again and check before accepting.'
            : 'JEV checked the walkthrough: no findings. Accept and build when you are satisfied.';
          // An explicit Check that blocks on findings lands focus on the
          // real findings; the near-top summary keeps the state, action and
          // a second link visible without scrolling.
          if (findings > 0) this.focusFindings();
          return;
        }
        if (planned.review?.consistency?.mismatches.length) {
          this.planNotice =
            'The authored walkthrough needs a correction. Correct or clarify the request above (your objective and choices stay with your authorship), then plan again and check before accepting.';
          return;
        }
        if (!planned.review?.consistency) {
          this.planNotice = 'Check the walkthrough again before accepting this direction.';
          return;
        }
      }
      if (this.revision?.state !== 'accepted' || this.revision.repository !== repository)
        await this.save('accepted', repository, signal);
      await this.generate(signal);
      signal.throwIfAborted();
      this.dispatchEvent(
        new CustomEvent('intent-accepted', {
          detail: {intent: this.revision?.intent, revision: this.revision?.id, repository, state: 'accepted'},
          bubbles: true,
          composed: true,
        })
      );
    });
  }
  private generationAddress(): RequestDraftAddress | undefined {
    const address = this.draftAddress();
    return address.subject === 'new' || !this.actorValid() ? undefined : address;
  }
  private generationPath(revision: IntentRevision): string {
    return '/api/factory/intents/' + revision.intent + '/revisions/' + revision.id + '/generation';
  }
  private async generate(signal: AbortSignal) {
    const accepted = this.revision,
      address = this.generationAddress();
    if (!accepted || accepted.state !== 'accepted' || !address) throw Error('Product direction not accepted');
    const writing = this.captureDraft();
    try {
      const current = readGeneration(address);
      if (current.blocked || current.pending) {
        this.generationCommandNotice =
          'An earlier work-creation request needs confirmation. Current writing is kept; check its original result before continuing.';
        return;
      }
      const host = await this.saveIdentity(signal);
      const prior = generationReceipt(await this.call(this.generationPath(accepted), undefined, signal), accepted);
      const pending: PendingGeneration = {
        host,
        revision: structuredClone(accepted),
        operation: prior.operation,
        writing,
      };
      if (!(await retainGeneration(address, pending))) {
        this.generationCommandNotice =
          'The exact work-creation request could not be retained durably, or another request is pending. No command was sent. Current writing is kept.';
        return;
      }
      signal.throwIfAborted();
      this.generationCanResume = this.generationCanRelease = false;
      this.generationCommandNotice =
        'The original work-creation request is awaiting confirmation. Check its saved result if the reply is lost.';
      const body = await this.call(
        '/api/factory/intents/' + accepted.intent + '/generate',
        {revision: accepted.id},
        signal
      );
      if (planWait(body)) {
        this.generationNotice = generationWaitNotice(body);
        return;
      }
      const receipt = generationReceipt(body, accepted, pending.operation);
      await this.confirmGeneration(address, pending, receipt, signal);
    } catch (error) {
      signal.throwIfAborted();
      this.generationNotice = generationFailureNotice(error);
    }
  }
  private async confirmGeneration(
    address: RequestDraftAddress,
    pending: PendingGeneration,
    receipt: GenerationReceipt,
    signal: AbortSignal
  ) {
    if (receipt.state === 'not_requested' || receipt.state === 'pending' || receipt.state === 'committed') {
      this.generationCommandNotice =
        receipt.state === 'not_requested'
          ? 'No work creation is recorded for the original revision yet. It may still finish; check again before explicitly resuming that same request.'
          : 'The original work-creation operation is recorded, but its final outcome is not confirmed. Check again or explicitly resume that same operation.';
      this.generationCanResume = true;
      return;
    }
    if (!(await settleGeneration(address, pending))) {
      signal.throwIfAborted();
      this.generationCommandNotice =
        'The original result was confirmed, but its writing could not be archived or its pending record cleared. Current writing and the saved command are kept; check again after storage is available.';
      return;
    }
    signal.throwIfAborted();
    this.generationCanResume = this.generationCanRelease = false;
    this.generationCommandNotice =
      receipt.state === 'completed'
        ? 'The original work-creation request is recorded as completed. Current writing is kept.'
        : 'The original work-creation request was refused. Current writing is kept.';
    if (this.revision?.id === pending.revision.id && this.revision.intent === pending.revision.intent) {
      this.generation = intentGeneration(receipt.raw);
      this.generationIntent = pending.revision.id;
      this.generationNotice = generationMessage(receipt.state);
    }
  }
  private recoverGeneration = (resume = false, release = false) => {
    void this.perform(
      async (signal) => {
        try {
          const address = this.generationAddress();
          if (!address) throw Error('Missing work-creation scope');
          const read = readGeneration(address),
            pending = read.pending;
          if (read.blocked || !pending) throw Error('Work-creation record unavailable');
          check((await this.saveIdentity(signal)) === pending.host);
          const retained = readGeneration(address);
          check(!retained.blocked && retained.pending && JSON.stringify(retained.pending) === JSON.stringify(pending));
          let head: IntentRevision | undefined;
          if (release) {
            head = intentRevision(
              await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
            );
            check(head.intent === pending.revision.intent && decisionID(head.id));
          }
          const body = resume
            ? await this.call(
                '/api/factory/intents/' + pending.revision.intent + '/generate',
                {revision: pending.revision.id},
                signal
              )
            : await this.call(this.generationPath(pending.revision), undefined, signal);
          if (planWait(body)) {
            this.generationCommandNotice = generationWaitNotice(body);
            return;
          }
          const receipt = generationReceipt(body, pending.revision, pending.operation);
          if (receipt.state === 'not_requested') {
            if (!head)
              head = intentRevision(
                await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
              );
            check(head.intent === pending.revision.intent && decisionID(head.id));
            this.generationCanResume = head.id === pending.revision.id;
            this.generationCanRelease = !this.generationCanResume;
            if (release && this.generationCanRelease) {
              // Absence was read after the advanced head: the durable-before-effect
              // store fence prevents a late original command from creating work.
              check(await settleGeneration(address, pending));
              signal.throwIfAborted();
              this.generationCanRelease = false;
              this.generationCommandNotice =
                'The superseded request had no recorded generation and was released. Current writing is kept.';
            } else
              this.generationCommandNotice = this.generationCanRelease
                ? 'The original revision was superseded and no generation was recorded. Release this saved request explicitly to continue with a newer direction; current writing is kept.'
                : 'No work creation is recorded for the original revision yet. It may still finish; check again or explicitly resume that same request.';
            return;
          }
          await this.confirmGeneration(address, pending, receipt, signal);
        } catch (error) {
          signal.throwIfAborted();
          this.generationCanResume = this.generationCanRelease = false;
          this.generationCommandNotice =
            error instanceof SodaRequestError && error.code === 'stale_revision'
              ? 'The original accepted direction has changed. Check its recorded result before continuing with a newer direction.'
              : 'The original work-creation result could not be verified. Current writing and the saved request are kept; check again when access is available.';
        }
      },
      resume ? 'Resuming the original work-creation request…' : 'Checking the original work-creation result…'
    );
  };
  private generationRecovery() {
    const address = this.generationAddress();
    if (!address) return '';
    const read = readGeneration(address);
    if (!read.pending && !read.blocked && !this.generationCommandNotice) return '';
    return html`<section aria-label="Work creation recovery">
      <p role="status">
        ${read.blocked ? 'Saved work-creation ownership or contents could not be verified. Its bytes were preserved; no new command can be sent.' : this.generationCommandNotice || 'An earlier work-creation request needs confirmation. No command was replayed.'}
      </p>
      ${
        read.pending
          ? html`<label
                >Original requested outcome<textarea readonly .value=${read.pending.revision.objective}></textarea>
              </label>
              ${read.pending.writing.changeText ? html`<label>Original request notes<textarea readonly .value=${read.pending.writing.changeText}></textarea></label>` : ''}
              <button
                type="button"
                ?disabled=${this.busy || this.blocked || !this.active}
                @click=${() => this.recoverGeneration()}
              >
                Check work creation
              </button>
              ${this.generationCanResume ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverGeneration(true)}>Resume original work creation</button>` : ''}
              ${this.generationCanRelease ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverGeneration(false, true)}>Release superseded request</button>` : ''} `
          : ''
      }
    </section>`;
  }
  private planWork = () => {
    const revision = this.revision;
    if (!revision || revision.state !== 'draft' || this.dirty) return;
    if (this.plan?.revision === revision.id && this.plan.state === 'pending') return;
    void this.perform((signal) => this.planLiveAttempt(revision, signal), 'Starting planning…');
  };
  // One settled-retry contract for both planning buttons: the retry
  // decision never comes from cached state. A failed fetch, a mismatched
  // revision, a live pending state or attention that asks a product
  // question stops with truthful guidance and never POSTs. Only a verified
  // settled terminal plan (failed, cancelled, a planner that stopped
  // without a usable answer, or completed without walkthroughs) authors a
  // successor draft; only a verified none plans this revision directly.
  // The save preserves the direction, choices and finite limits; the
  // settled history stays recorded and planning targets the new revision.
  private async planLiveAttempt(revision: IntentRevision, signal: AbortSignal) {
    if (this.explorationBlocked() || this.planningBlocked()) return;
    let fresh;
    try {
      fresh = intentPlanStatus(await this.call('/api/factory/intents/' + revision.intent + '/plan', undefined, signal));
    } catch {
      if (signal.aborted) return;
      this.planNotice =
        'Planning status could not be verified. Your request is saved; refresh status before trying again.';
      return;
    }
    signal.throwIfAborted();
    if (fresh.revision !== revision.id) {
      this.planNotice = 'Planning status answers another draft. Refresh status before trying again.';
      return;
    }
    this.plan = fresh;
    if (fresh.state === 'none') return this.startPlanning(revision, signal);
    if (
      fresh.state === 'failed' ||
      fresh.state === 'cancelled' ||
      plannerStopped(fresh) ||
      (fresh.state === 'completed' && !fresh.walkthroughs)
    ) {
      const bound = await this.save('draft', revision.repository, signal);
      signal.throwIfAborted();
      this.dirty = false;
      this.dispatchEvent(
        new CustomEvent('intent-saved', {
          detail: {intent: bound.intent, revision: bound.id, state: bound.state},
          bubbles: true,
          composed: true,
        })
      );
      await this.startPlanning(bound, signal);
      return;
    }
    if (fresh.state === 'pending') {
      this.planNotice = 'Your request is saved. Its planning progress and any question appear here.';
      return;
    }
    if (fresh.state === 'attention') {
      this.planNotice = attentionText(fresh);
      return;
    }
    // A completed plan with walkthroughs is ready for the review flow
    // below; the retry action leaves that state and its guidance alone.
  }
  private planningBlocked(): boolean {
    const address = this.generationAddress();
    if (!address) return false;
    const read = readPlanning(address);
    if (!read.pending && !read.blocked) return false;
    this.planningCommandNotice =
      'An earlier planning request needs confirmation. Current writing is kept; check its original result before saving or planning again.';
    return true;
  }
  private planningPath(revision: IntentRevision): string {
    return '/api/factory/intents/' + revision.intent + '/revisions/' + revision.id + '/plan';
  }
  private async startPlanning(revision: IntentRevision, signal: AbortSignal) {
    if (this.planningBlocked()) return;
    const operation = this.planOperation === 'decomposition_options' ? 'decomposition_options' : 'propose_intent';
    const address = this.generationAddress();
    if (!address) throw Error('Missing planning scope');
    const writing = this.captureDraft();
    try {
      const host = await this.saveIdentity(signal);
      let pending: PendingPlanning = {
        host,
        revision: structuredClone(revision),
        operation,
        writing,
        assignment: '',
        run: '',
        launch: 'unknown',
      };
      const prior = planningReceipt(await this.call(this.planningPath(revision), undefined, signal), pending);
      if (prior.state !== 'none') pending = {...pending, assignment: prior.assignment!, run: prior.run!};
      if (!(await retainPlanning(address, pending))) {
        this.planningCommandNotice =
          'The planning request could not be saved on this device, or another request is pending. No planning command was sent; current writing is kept.';
        return;
      }
      signal.throwIfAborted();
      this.planningCanResume = this.planningCanRelease = false;
      this.planningCommandNotice =
        'The original planning request is awaiting confirmation. Check its recorded result if the reply is lost.';
      const outcome = await runPlanWork(
        async () => {
          const posted = await this.call(
            '/api/factory/intents/' + revision.intent + '/plan',
            planActionBody(revision.id, operation),
            signal
          );
          if (planWait(posted)) return posted;
          const bound = planningLaunch(posted, pending);
          if (!(await bindPlanning(address, pending, bound))) {
            signal.throwIfAborted();
            this.planningCommandNotice =
              'The planning reply was received, but its run identity could not be saved durably. Keep this window open and check the original result after storage is available.';
            throw Error('Planning identity persistence failed');
          }
          pending = bound;
          return posted;
        },
        async () => {
          const raw = await this.call(this.planningPath(revision), undefined, signal);
          const receipt = planningReceipt(raw, pending);
          check(receipt.state !== 'none');
          if (!(await settlePlanning(address, pending))) throw Error('Planning archive persistence failed');
          signal.throwIfAborted();
          return raw;
        },
        revision.id
      );
      signal.throwIfAborted();
      this.planningCommandNotice = outcome.status
        ? ''
        : 'Planning is waiting for preparation. Its exact request is kept; check before explicitly resuming.';
      this.planNotice =
        outcome.status?.state === 'attention' && outcome.notice.startsWith('Planning needs a product decision')
          ? attentionText(outcome.status)
          : outcome.notice;
      this.generationNotice = '';
      if (outcome.status !== undefined) {
        this.plan = outcome.status;
        this.planIntent = revision.id;
      }
    } catch (error) {
      if (signal.aborted) return;
      this.planNotice =
        error instanceof SodaRequestError && error.code === 'planning_unavailable'
          ? 'Planning is unavailable. Open Prepare project to check its environment and model account. Your request is saved.'
          : 'Planning could not be confirmed. Your request is saved; check the original planning result before trying again.';
    }
  }
  private recoverPlanning = (resume = false, release = false) => {
    void this.perform(
      async (signal) => {
        try {
          const address = this.generationAddress();
          if (!address) throw Error('Missing planning scope');
          const read = readPlanning(address);
          let pending = read.pending;
          if (read.blocked || !pending) throw Error('Planning record unavailable');
          check((await this.saveIdentity(signal)) === pending.host);
          const retained = readPlanning(address);
          check(retained.pending && JSON.stringify(retained.pending) === JSON.stringify(pending));
          let head: IntentRevision | undefined;
          if (release) {
            head = intentRevision(
              await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
            );
            check(head.intent === pending.revision.intent && decisionID(head.id));
          }
          if (resume) {
            const posted = await this.call(
              '/api/factory/intents/' + pending.revision.intent + '/plan',
              planActionBody(pending.revision.id, pending.operation),
              signal
            );
            if (planWait(posted)) {
              this.planningCommandNotice = 'Planning is still waiting for preparation. The original request is kept.';
              return;
            }
            const bound = planningLaunch(posted, pending);
            await bindPlanning(address, pending, bound);
            signal.throwIfAborted();
            const observed = readPlanning(address);
            check(
              !observed.blocked &&
                observed.pending &&
                observed.pending.assignment === bound.assignment &&
                observed.pending.run === bound.run
            );
            pending = observed.pending;
          }
          const receipt = planningReceipt(
            await this.call(this.planningPath(pending.revision), undefined, signal),
            pending
          );
          if (receipt.state === 'none') {
            if (!head)
              head = intentRevision(
                await this.call('/api/factory/intents/' + pending.revision.intent, undefined, signal)
              );
            check(head.intent === pending.revision.intent && decisionID(head.id));
            this.planningCanResume = head.id === pending.revision.id && !pending.assignment;
            this.planningCanRelease = head.id !== pending.revision.id && !pending.assignment;
            if (release && this.planningCanRelease) {
              check(await settlePlanning(address, pending));
              signal.throwIfAborted();
              this.planningCanRelease = false;
              this.planningCommandNotice =
                'The superseded request had no recorded planning attempt and was released. Current writing is kept.';
            } else
              this.planningCommandNotice =
                'The original planning attempt is not recorded yet. It may still finish. Current writing and the exact request are kept.';
            return;
          }
          if (!pending.assignment) {
            const bound = {...pending, assignment: receipt.assignment!, run: receipt.run!};
            await bindPlanning(address, pending, bound);
            signal.throwIfAborted();
            const observed = readPlanning(address);
            check(
              !observed.blocked &&
                observed.pending &&
                observed.pending.assignment === bound.assignment &&
                observed.pending.run === bound.run
            );
            pending = observed.pending;
          }
          if (!(await settlePlanning(address, pending))) {
            this.planningCommandNotice =
              'The original planning result is recorded, but its writing or pending identity could not be saved. Both copies are kept; check again after storage is available.';
            return;
          }
          signal.throwIfAborted();
          this.planningCanResume = this.planningCanRelease = false;
          this.planningCommandNotice =
            'The original planning request is recorded. Current writing is kept; no new run was started by this check.';
          if (this.revision?.id === pending.revision.id && this.revision.intent === pending.revision.intent) {
            this.plan = receipt;
            this.planIntent = pending.revision.id;
            this.planNotice =
              receipt.state === 'pending'
                ? 'Planning is recorded. This read does not confirm native start; check the existing run for progress.'
                : receipt.state === 'failed'
                  ? planningFailedNotice
                  : receipt.state === 'cancelled'
                    ? planningCancelledNotice
                    : receipt.state === 'attention'
                      ? attentionText(receipt)
                      : '';
          }
        } catch {
          signal.throwIfAborted();
          this.planningCanResume = this.planningCanRelease = false;
          this.planningCommandNotice =
            'The original planning result could not be verified. Current writing and its exact saved request are kept; check again when access is available.';
        }
      },
      resume ? 'Resuming the original planning request…' : 'Checking the original planning result…'
    );
  };
  private planningRecovery() {
    const address = this.generationAddress();
    if (!address) return '';
    const read = readPlanning(address);
    if (!read.pending && !read.blocked && !this.planningCommandNotice) return '';
    return html`<section aria-label="Planning recovery">
      <p role="status">
        ${read.blocked ? 'Saved planning ownership or contents could not be verified. Its bytes were preserved; no new planning command can be sent.' : this.planningCommandNotice || 'An earlier planning request needs confirmation. No command was replayed.'}
      </p>
      ${
        read.pending
          ? html`<label
                >Original planning outcome<textarea readonly .value=${read.pending.revision.objective}></textarea>
              </label>
              ${!read.durable ? html`<p role="status">Observed run identities are kept in this window only. Copy the details before closing or reloading.</p>` : ''}
              ${read.pending.assignment ? html`<p>Recorded assignment: ${read.pending.assignment}<br />Recorded run: ${read.pending.run}</p>` : ''}
              <button
                type="button"
                ?disabled=${this.busy || this.blocked || !this.active}
                @click=${() => this.recoverPlanning()}
              >
                Check planning result
              </button>
              ${this.planningCanResume ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverPlanning(true)}>Resume original planning</button>` : ''}
              ${this.planningCanRelease ? html`<button type="button" ?disabled=${this.busy || this.blocked || !this.active} @click=${() => this.recoverPlanning(false, true)}>Release superseded planning</button>` : ''} `
          : ''
      }
    </section>`;
  }
  private prepareProject = () => {
    const revision = this.revision;
    if (!this.requestLive(this.requestEpoch) || !revision || revision.repository === '0' || this.busy) return;
    this.dispatchEvent(
      new CustomEvent('intent-preparation', {
        detail: {intent: revision.intent, repository: revision.repository},
        bubbles: true,
        composed: true,
      })
    );
  };
  private refreshPlanStatus = () => {
    this.planIntent = '';
    this.fetchPlanStatus(true);
  };
  private planAdmitted(revision: IntentRevision, refresh: boolean): boolean {
    if (!this.api || !this.active || this.blocked) return false;
    if (!refresh && this.planIntent === revision.id) return false;
    return true;
  }
  private fetchPlanStatus(refresh = false) {
    const revision = this.revision;
    if (!revision || revision.state !== 'draft' || revision.repository === '0' || !this.planAdmitted(revision, refresh))
      return;
    void this.loadPlanStatus(revision);
  }
  private loadPlanStatus(revision: IntentRevision) {
    const id = revision.id;
    void this.perform(async (signal) => {
      try {
        const status = intentPlanStatus(
          await this.call('/api/factory/intents/' + revision.intent + '/plan', undefined, signal)
        );
        if (status.revision !== id) throw Error('Mismatched plan status');
        if (this.revision?.id !== id || signal.aborted) return;
        this.planIntent = id;
        this.applyPolledPlanStatus(status, id);
      } catch {
        if (!signal.aborted) {
          this.planIntent = id;
          if (this.planNotice === '')
            this.planNotice = 'Planning status could not be loaded. Your direction is saved; try again.';
        }
      }
    }, 'Loading planning status…');
  }
  private continue = () => {
    const revision = this.revision;
    if (!this.requestLive(this.requestEpoch) || !revision || !reviewReady(this.view())) return;
    if (revision.repository !== '0') {
      void this.acceptRepository(revision.repository);
      return;
    }
    const resolved = this.resolvedScope(revision);
    this.dispatchEvent(
      new CustomEvent('intent-reviewed', {
        detail: {
          intent: revision.intent,
          revision: revision.id,
          approach: revision.approach,
          authorizedIdeas: resolved.length,
          tasks: revision.tasks.length,
          decisions: resolved.map((idea) => ({idea: idea.id, decision: idea.decision})),
        },
        bubbles: true,
        composed: true,
      })
    );
  };
  private actorValid(): boolean {
    return this.actor !== '' && id(this.actor);
  }
  private settledIDs(): Set<string> {
    return new Set(this.exploration?.settled || []);
  }
  private decidedIdea(idea: ProductIdea, decision: ProductIdea['decision'], correction?: string): ProductIdea {
    if (decision === 'edit' && !(correction ?? '').trim() && this.settledIDs().has(idea.id)) {
      const restored: ProductIdea = {...idea, decision: 'accept', provenance: 'autonomous'};
      delete restored.correction;
      delete restored.actor;
      return restored;
    }
    const next: ProductIdea = {...idea, decision};
    if (correction !== undefined) next.correction = correction;
    else if (decision !== 'edit') delete next.correction;
    if (decision === 'pending' || !this.actorValid()) {
      delete next.provenance;
      delete next.actor;
    } else {
      next.provenance = 'human';
      next.actor = this.actor;
    }
    return next;
  }
  private decide = (id: string, decision: ProductIdea['decision'], correction?: string) => {
    if (!this.revision || this.busy || this.blocked) return;
    this.revision = {
      ...this.revision,
      ideas: this.revision.ideas.map((idea) => (idea.id === id ? this.decidedIdea(idea, decision, correction) : idea)),
    };
    this.dirty = true;
    this.rememberDraft();
  };
  private correctProposal = (text: string) => {
    const correction = text.trim().slice(0, 4000);
    if (!correction || !this.revision || this.busy || this.blocked) return;
    const idea: ProductIdea = {
      id: newDecisionID(),
      description: correction,
      consequence: 'The proposal is updated to match the requested change.',
      assumption: false,
      decision: 'edit',
      correction,
      source: 'requested',
      ...(this.actorValid() ? {provenance: 'human' as const, actor: this.actor} : {}),
    };
    this.revision = {...this.revision, ideas: [...this.revision.ideas, idea]};
    this.revise();
  };
  private toggleChange = (open: boolean) => {
    const epoch = this.requestEpoch;
    this.changeOpen = open;
    if (open)
      void this.updateComplete.then(() => {
        if (!this.requestLive(epoch)) return;
        this.shadowRoot?.querySelector<HTMLElement>('.change-field textarea')?.focus();
      });
  };
  private createDepartment = () => {
    void this.perform(async (signal) => {
      const value = department(
        await this.call(
          '/api/factory/departments',
          {id: newDecisionID(), name: this.departmentName.trim(), description: this.departmentDescription.trim()},
          signal
        )
      );
      signal.throwIfAborted();
      this.departments = [...this.departments, value];
      this.department = value.id;
    }).then((ok) => {
      if (ok) this.toggleDepartment(false);
    });
  };
  private toggleDepartment = (open: boolean) => {
    const epoch = this.requestEpoch;
    this.departmentDialog = open;
    void this.updateComplete.then(() => {
      if (!this.requestLive(epoch)) return;
      const selector = open ? '.department-dialog input' : '.open-department';
      this.shadowRoot?.querySelector<HTMLElement>(selector)?.focus();
    });
  };
  private dialogKey = (event: KeyboardEvent) => {
    if (event.key === 'Escape' && !this.busy) {
      event.preventDefault();
      this.toggleDepartment(false);
    }
    if (event.key !== 'Tab') return;
    this.trapDialogTab(event);
  };
  private trapDialogTab(event: KeyboardEvent) {
    const controls = Array.from(
      this.shadowRoot?.querySelectorAll<HTMLElement>(
        '.department-dialog input, .department-dialog textarea, .department-dialog button:not([disabled])'
      ) || []
    );
    const boundary = event.shiftKey ? controls[0] : controls.at(-1);
    if (this.shadowRoot?.activeElement !== boundary) return;
    event.preventDefault();
    (event.shiftKey ? controls.at(-1) : controls[0])?.focus();
  }
  private ideaChanged(idea: ProductIdea): boolean {
    const saved = this.savedSnapshot?.ideas.find((entry) => entry.id === idea.id);
    return !saved || saved.decision !== idea.decision || (saved.correction || '') !== (idea.correction || '');
  }
  private unsavedScalars(snapshot: IntentRevision): {count: number; parts: string[]} {
    const parts: string[] = [];
    let count = 0;
    if (this.objective.trim() !== snapshot.objective) {
      count += 1;
      parts.push('outcome text');
    }
    if ((this.department || '') !== (snapshot.department || '')) {
      count += 1;
      parts.push('department');
    }
    if (this.maxIssues !== snapshot.max_issues) {
      count += 1;
      parts.push('planning limits');
    }
    return {count, parts};
  }
  private unsavedSummary(): {count: number; parts: string} {
    const snapshot = this.savedSnapshot;
    const revision = this.revision;
    if (!snapshot || !revision || !this.dirty) return {count: 0, parts: ''};
    const scalars = this.unsavedScalars(snapshot);
    const decided = revision.ideas.filter((idea) => this.ideaChanged(idea)).length;
    if (decided) {
      scalars.count += decided;
      scalars.parts.push(`${decided} idea decision${decided === 1 ? '' : 's'}`);
    }
    return {count: scalars.count, parts: scalars.parts.join(', ')};
  }
  private view(): IntentView {
    const unsaved = this.unsavedSummary();
    return {
      ...(this.project ? {project: this.project} : {}),
      editRequest: this.editRequest,
      objective: this.objective,
      department: this.department,
      departments: this.departments,
      revision: this.revision,
      exploration: this.exploration,
      explorationStale: this.explorationStale(),
      generation: this.generation,
      generationError: this.generationError,
      generationNotice: this.generationNotice,
      unsavedCount: unsaved.count,
      unsavedParts: unsaved.parts,
      changeOpen: this.changeOpen,
      changeText: this.changeText,
      busy: this.busy,
      blocked: this.blocked || !this.active,
      dirty: this.dirty,
      error: this.error,
      departmentDialog: this.departmentDialog,
      departmentName: this.departmentName,
      departmentDescription: this.departmentDescription,
      maxIssues: this.maxIssues,
      prepareProject: this.prepareProject,
      set: (field, value) => {
        this[field] = value;
        if ((field === 'objective' || field === 'department') && this.revision) this.dirty = true;
        this.rememberDraft();
      },
      setMaxIssues: (value) => {
        this.maxIssues = value;
        if (this.revision) this.dirty = true;
        this.rememberDraft();
      },
      decide: this.decide,
      toggleChange: this.toggleChange,
      correctProposal: this.correctProposal,
      refreshGeneration: () => this.fetchGeneration(true),
      chooseApproach: (approach) => {
        if (!this.revision) return;
        this.revision = {...this.revision, approach};
        this.dirty = true;
        this.rememberDraft();
      },
      describe: this.describe,
      revise: this.revise,
      decline: this.decline,
      continue: this.continue,
      dialog: this.toggleDepartment,
      dialogKey: this.dialogKey,
      createDepartment: this.createDepartment,
      planOperation: this.planOperation,
      plan: this.plan?.revision === this.revision?.id ? this.plan : undefined,
      planNotice: this.planNotice,
      focusFindings: () => this.focusFindings(),
      setPlanOperation: (value: string) => {
        this.planOperation = value;
      },
      planWork: this.planWork,
      refreshPlanStatus: this.refreshPlanStatus,
    };
  }
  private reviewing() {
    const state = this.revision?.state;
    return !!this.revision?.ideas.length && (state === 'draft' || state === 'accepted');
  }
  protected render() {
    const view = this.view();
    const status = this.busy ? this.busyNote : this.error;
    return html`<div .inert=${this.departmentDialog}>
        ${this.reviewing() ? reviewIntent(view) : describeIntent(view)}
      </div>
      <p role="status" class="intent-live-status">${status ? html`<span>${status}</span>` : ''}</p>
      ${
        this.draftNotice
          ? html`<section aria-label="Request draft recovery">
              <p role="status">${this.draftNotice}</p>
              <button
                type="button"
                ?disabled=${this.busy || this.blocked || !this.active}
                @click=${() => this.rememberDraft()}
              >
                Try saving draft again
              </button>
              ${
                this.draftPreview
                  ? html`<details>
                      <summary>Preserved saved writing</summary>
                      <label
                        >Preserved saved outcome<textarea readonly .value=${this.draftPreview.objective}></textarea>
                      </label>
                      ${this.draftPreview.changeText ? html`<label>Preserved saved change notes<textarea readonly .value=${this.draftPreview.changeText}></textarea></label>` : ''}
                    </details>`
                  : ''
              }
            </section>`
          : ''
      }
      ${this.saveRecovery()} ${this.explorationRecovery()} ${this.planningRecovery()} ${this.generationRecovery()}
      ${this.scopeRecovery()} ${this.declinedWorkRecovery()} ${this.savedHistoryRecovery()}
      ${this.unresolvedRepairReview()} ${departmentDialog(view)}`;
  }
}
customElements.define('soda-intent', SodaIntent);

// Saved-history traversal bound: the exact predecessor first, then at most
// this many predecessor hops back along the current head's chain. Anything
// older, off-chain, or meaning-changed is reported unavailable instead of
// merged.
const savedHistoryHopBound = 8;

const savedHistoryNoPredecessor =
  'This saved request names no earlier saved revision, so there is no saved history to preview. Only newly saved revisions can supply recovery.';
const savedHistoryUnavailable =
  'No saved history can restore this declined work. Only the exact saved predecessor chain with the same idea and its recorded work can supply it; unsent edits stay on this device and nothing was restored.';
const savedHistoryPartial =
  'Saved history holds some of the declined work above. The remaining ideas name no matching saved graph, so nothing more was restored.';

// One staged history row: the recorded tasks for a declined idea, repairs
// for recorded kept-edge cuts into the recovered set, edges refused as
// unprovable, and the source revision's idea snapshot the meaning checks
// ran against (re-verified at preview and re-accept time).
interface SavedHistoryRepair {
  owner: string;
  deps: string[];
  ideas: string[];
  depIdeas: Record<string, string[]>;
  targets: string[];
  resolved: boolean;
}
interface SavedHistoryCutEdge {
  key: string;
  objective: string;
  dep: string;
  kind: 'task' | 'repair';
}
interface SavedHistoryEntry {
  idea: string;
  revision: string;
  tasks: IntentTask[];
  repairs: SavedHistoryRepair[];
  cutEdges: SavedHistoryCutEdge[];
  ideas: ProductIdea[];
}

// Idea meaning for history recovery: identity plus the effective semantic
// content. A decision is workflow state, not meaning, so it is compared by
// the callers (the recovered idea is declined by definition); the edit
// correction is meaning and always counts.
//
// A correction missing on a non-edit side is the Decline/accept/pending
// cleanup (decidedIdea deletes it) of a duplicated description, not a human
// meaning change: the shared description still carries the effective
// meaning, so it stays matchable (FJ-453: a user-created
// change→Decline→save→cross-device recovery must work — correctProposal
// creates the idea with description === correction, so the Decline-deleted
// correction exactly duplicates the shared description). A deleted
// substantive correction (one that says something the shared description
// does not, e.g. historical “restricted to requester/manager” against
// shared “public access”) is a meaning change and stays a mismatch (FJ-457),
// as does a correction that differs while both sides stay edits or any
// description/consequence change. Nothing is merged or invented here.
function sameIdeaMeaning(past: ProductIdea, now: ProductIdea): boolean {
  if (past.description !== now.description || past.consequence !== now.consequence) return false;
  const pastCorrection = past.correction || '';
  const nowCorrection = now.correction || '';
  if (pastCorrection === nowCorrection) return true;
  // Both sides carry a correction yet disagree: a human changed the meaning.
  if (pastCorrection !== '' && nowCorrection !== '') return false;
  // Exactly one side is empty. Match only when the empty side is a non-edit
  // idea whose correction the workflow transition itself deleted AND the
  // deleted correction exactly duplicates the shared description; an edit
  // idea with an emptied correction is a human change, never a match, and
  // so is a deleted substantive correction.
  const emptySide = pastCorrection === '' ? past : now;
  const deletedCorrection = pastCorrection === '' ? nowCorrection : pastCorrection;
  return emptySide.decision !== 'edit' && deletedCorrection === past.description;
}
// A recorded task link is replayable only when the linked idea still exists
// with the same meaning and, beyond the recovered idea itself, is not
// declined in the current head.
function historyLinkOK(
  link: string,
  selected: string,
  past: Map<string, ProductIdea>,
  now: Map<string, ProductIdea>
): boolean {
  const was = past.get(link);
  const is = now.get(link);
  return (
    was !== undefined &&
    is !== undefined &&
    sameIdeaMeaning(was, is) &&
    (link === selected || is.decision !== 'decline')
  );
}

// Owner-private history read: full newest-first immutable revisions for one
// request. Strictly validated like any save: a malformed page degrades to
// the honest unavailable state, never to partial guesses. The bare endpoint
// serves the default page; no limit parameter is sent.
function intentHistory(value: unknown): IntentRevision[] {
  const raw = object(value);
  check(decisionID(raw.intent));
  const revisions = raw.revisions;
  check(Array.isArray(revisions) && revisions.length <= 64);
  return (revisions as unknown[]).map((entry) => intentRevision(entry));
}

export interface RestoredReviewIdentity {
  basis: string;
  result: string;
}
export function adoptReview(read: SavedReviewRead | undefined): {
  exploration: Exploration | undefined;
  restored: RestoredReviewIdentity | undefined;
} {
  const review = read?.review;
  if (!review) return {exploration: undefined, restored: undefined};
  const {content, challenges, settled, recommendation, consistency} = review;
  return {
    exploration: {
      content,
      challenges,
      settled,
      ...(recommendation ? {recommendation} : {}),
      ...(consistency ? {consistency} : {}),
    },
    restored: {basis: review.basis_revision, result: review.result_revision},
  };
}
// restoredStale reports whether a basis/result-tracked exploration is out of
// date: local edits always stale it, otherwise the stored result must still
// be the head. Allowance and other non-head changes never stale it.
export function restoredStale(dirty: boolean, result: string, revisionId: string): boolean {
  if (dirty) return true;
  return result !== revisionId;
}

function generationMessage(state: unknown): string {
  if (state === 'completed') return 'Factory work is created. Execution still needs its own access and finite funding.';
  if (state === 'pending' || state === 'committed')
    return 'Your product direction is saved. Factory is waiting for confirmation of the native work.';
  check(state === 'refused');
  return 'Your product direction is saved. Factory could not create the native work; an administrator must inspect the refusal.';
}

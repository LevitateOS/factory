import type {IntentRevision, IntentTask, ProductIdea} from '../api/request-response.js';
import type {ShelvedDepRepair} from './draft.js';

export interface TaskShelfSnapshot {
  shelf: IntentTask[];
  shelfDeps: Record<string, ShelvedDepRepair>;
}

/** Retains declined work and its exact dependency lineage across saves and
 * regenerated task keys. Ambiguity stays unresolved rather than inventing edges. */
export class TaskShelf {
  private shelvedTasks: IntentTask[] = [];
  private shelvedPrunedDeps: Record<string, ShelvedDepRepair> = {};
  private pendingDropped: IntentTask[] = [];
  private pendingRestored: IntentTask[] = [];
  get tasks(): readonly IntentTask[] {
    return this.shelvedTasks;
  }
  snapshot(): TaskShelfSnapshot {
    return structuredClone({shelf: this.shelvedTasks, shelfDeps: this.shelvedPrunedDeps});
  }
  restore(saved: TaskShelfSnapshot) {
    this.shelvedTasks = structuredClone(saved.shelf);
    this.shelvedPrunedDeps = structuredClone(saved.shelfDeps);
  }
  reset(saved: TaskShelfSnapshot = {shelf: [], shelfDeps: {}}) {
    this.restore(saved);
    this.clearProposal();
  }
  clearProposal() {
    this.pendingDropped = [];
    this.pendingRestored = [];
  }
  proposal(ideas: ProductIdea[], tasks: IntentTask[]): IntentTask[] {
    const split = this.splitProposalTasks(ideas, tasks);
    this.pendingDropped = split.dropped;
    this.pendingRestored = split.restored;
    return split.tasks;
  }
  afterSave(next: IntentRevision, revision: IntentRevision | undefined): TaskShelfSnapshot {
    const after = new TaskShelf();
    after.restore(this.snapshot());
    after.pendingDropped = this.pendingDropped;
    after.pendingRestored = this.pendingRestored;
    after.shelveDroppedTasks(next, revision);
    return after.snapshot();
  }
  addRecovered(task: IntentTask) {
    if (!this.shelvedTasks.some((row) => row.key === task.key)) this.shelvedTasks.push(task);
  }
  dropRepair(key: string) {
    delete this.shelvedPrunedDeps[key];
  }

  // Go IntentRevision validation rejects links to declined ideas. Recovery never re-decides them.
  private splitProposalTasks(
    ideas: ProductIdea[],
    tasks: IntentTask[]
  ): {tasks: IntentTask[]; dropped: IntentTask[]; restored: IntentTask[]} {
    const byID = new Map(ideas.map((idea) => [idea.id, idea]));
    const dropped: IntentTask[] = [];
    const kept = tasks.filter((task) => {
      if (!task.idea_ids.some((id) => byID.get(id)?.decision === 'decline')) return true;
      dropped.push(task);
      return false;
    });
    const restorable = this.shelvedTasks.filter(
      (task) =>
        !kept.some((entry) => entry.key === task.key) &&
        task.idea_ids.length > 0 &&
        task.idea_ids.every((id) => byID.has(id) && byID.get(id)?.decision !== 'decline')
    );
    const live = new Set([...kept.map((task) => task.key), ...restorable.map((task) => task.key)]);
    const repaired = kept.map((task) => this.repairTaskDeps(task, live));
    const pruned = repaired.map((task) => this.pruneTaskDeps(task, live));
    const restored = restorable.map((task) => this.pruneTaskDeps(this.repairTaskDeps(task, live), live));
    return {tasks: [...pruned, ...restored], dropped, restored: restorable};
  }

  private repairTaskDeps(task: IntentTask, live: Set<string>): IntentTask {
    const recorded = this.shelvedPrunedDeps[task.key]?.deps;
    if (!recorded || recorded.length === 0) return task;
    const current = new Set(task.depends_on || []);
    const repaired = recorded.filter((dep) => live.has(dep) && !current.has(dep));
    if (!repaired.length) return task;
    return {...task, depends_on: [...(task.depends_on || []), ...repaired]};
  }

  private pruneTaskDeps(task: IntentTask, live: Set<string>): IntentTask {
    const depends = task.depends_on || [];
    const kept = depends.filter((dep) => live.has(dep));
    return kept.length === depends.length ? task : {...task, depends_on: kept};
  }

  unresolved(revision: IntentRevision | undefined): {key: string; repair: ShelvedDepRepair}[] {
    const live = new Set((revision?.tasks || []).map((task) => task.key));
    const shelved = new Set(this.shelvedTasks.map((task) => task.key));
    return Object.entries(this.shelvedPrunedDeps)
      .filter(
        ([key, repair]) =>
          !live.has(key) &&
          !shelved.has(key) &&
          (repair.deps.length > 0 || Object.keys(repair.depIdeas || {}).length > 0)
      )
      .map(([key, repair]) => ({key, repair}));
  }

  private shelveDroppedTasks(next: IntentRevision, revision: IntentRevision | undefined) {
    const posted = new Set(next.ideas.map((idea) => idea.id));
    const saved = new Set(next.tasks.map((task) => task.key));
    const kept = this.shelvedTasks.filter(
      (task) => !saved.has(task.key) && task.idea_ids.length > 0 && task.idea_ids.every((id) => posted.has(id))
    );
    for (const task of this.pendingDropped) {
      if (saved.has(task.key) || kept.some((entry) => entry.key === task.key)) continue;
      kept.push(task);
    }
    this.shelvedTasks = kept;
    this.pendingDropped = [];
    const restored = new Map(this.pendingRestored.map((task) => [task.key, task]));
    this.pendingRestored = [];
    this.captureShelvedLineage(revision);
    this.recordPrunedDeps(next, restored, revision);
  }

  private captureShelvedLineage(revision: IntentRevision | undefined) {
    const known = new Map<string, string[]>();
    for (const task of revision?.tasks || []) known.set(task.key, [...task.idea_ids]);
    for (const task of this.shelvedTasks) known.set(task.key, [...task.idea_ids]);
    for (const task of this.shelvedTasks) {
      const depIdeas = lineageForDeps(
        task.depends_on || [],
        (dep) => known.get(dep),
        this.shelvedPrunedDeps[task.key]?.depIdeas
      );
      if (Object.keys(depIdeas).length === 0) continue;
      const prior = this.shelvedPrunedDeps[task.key];
      const deps = prior ? [...prior.deps] : [...(task.depends_on || [])];
      for (const dep of task.depends_on || []) if (!deps.includes(dep)) deps.push(dep);
      this.shelvedPrunedDeps[task.key] = {deps, ideas: [...task.idea_ids], depIdeas};
    }
    for (const [key, repair] of Object.entries(this.shelvedPrunedDeps)) {
      if (repair.deps.length === 0) continue;
      const depIdeas = lineageForDeps(repair.deps, (dep) => known.get(dep), repair.depIdeas);
      if (Object.keys(depIdeas).length === 0) continue;
      this.shelvedPrunedDeps[key] = {deps: [...repair.deps], ideas: [...repair.ideas], depIdeas};
    }
  }

  // A later decline starts from already-pruned saved work. Retained originals
  // must preserve earlier cuts and restored tasks absent from that revision.
  private recordPrunedDeps(
    next: IntentRevision,
    restored: Map<string, IntentTask>,
    revision: IntentRevision | undefined
  ) {
    const original = new Map((revision?.tasks || []).map((task) => [task.key, task]));
    const shelvedByKey = new Map(this.shelvedTasks.map((task) => [task.key, task]));
    const shelved = new Set(shelvedByKey.keys());
    const saved = new Set(next.tasks.map((task) => task.key));
    const posted = new Set(next.ideas.map((idea) => idea.id));
    const recorded: Record<string, ShelvedDepRepair> = {};
    for (const [key, repair] of Object.entries(this.shelvedPrunedDeps)) {
      if (!saved.has(key) && !shelved.has(key)) {
        if (repair.deps.length === 0 && Object.keys(repair.depIdeas || {}).length === 0) continue;
        if (!repair.ideas.some((id) => posted.has(id))) continue;
        recorded[key] = copyRepair(repair);
        continue;
      }
      if (shelved.has(key)) {
        if (repair.deps.length > 0 || Object.keys(repair.depIdeas || {}).length > 0) recorded[key] = copyRepair(repair);
        continue;
      }
      if (!repair.deps.some((dep) => shelved.has(dep)) && pendingRepairLineage(repair, saved, shelved).length === 0)
        continue;
      recorded[key] = copyRepair(repair);
    }
    for (const task of next.tasks) {
      const before = original.get(task.key) ?? restored.get(task.key);
      if ((before?.depends_on || []).length === 0 && !recorded[task.key]) continue;
      const initial = before?.depends_on || [];
      const after = new Set(task.depends_on || []);
      const cut = initial.filter((dep) => !after.has(dep));
      if (cut.some((dep) => shelved.has(dep))) {
        const prior = recorded[task.key];
        const depIdeas = lineageForDeps(
          initial,
          (dep) => (original.get(dep) ?? shelvedByKey.get(dep))?.idea_ids,
          prior?.depIdeas
        );
        if (prior) {
          const merged = [...prior.deps];
          for (const dep of initial) if (!merged.includes(dep)) merged.push(dep);
          const ideas = [...prior.ideas];
          for (const id of task.idea_ids) if (!ideas.includes(id)) ideas.push(id);
          const mergedLineage = {...prior.depIdeas};
          for (const [dep, ids] of Object.entries(depIdeas)) if (!mergedLineage[dep]) mergedLineage[dep] = ids;
          recorded[task.key] = {
            deps: merged,
            ideas,
            ...(Object.keys(mergedLineage).length > 0 ? {depIdeas: mergedLineage} : {}),
          };
        } else {
          recorded[task.key] = {
            deps: [...initial],
            ideas: [...task.idea_ids],
            ...(Object.keys(depIdeas).length > 0 ? {depIdeas} : {}),
          };
        }
      }
    }
    this.shelvedPrunedDeps = recorded;
  }

  // Regeneration can assign fresh keys. Only unique, identical idea linkage
  // identifies a successor; ambiguous records remain available for recovery.
  reconcile(proposal: IntentRevision): boolean {
    const ideas = new Set(proposal.ideas.map((idea) => idea.id));
    const live = new Set(proposal.tasks.map((task) => task.key));
    this.shelvedTasks = this.shelvedTasks.filter(
      (task) => !live.has(task.key) && task.idea_ids.length > 0 && task.idea_ids.every((id) => ideas.has(id))
    );
    const shelved = new Set(this.shelvedTasks.map((task) => task.key));
    const proposed = new Map(proposal.tasks.map((task) => [task.key, task]));
    const byIdeas = new Map<string, string[]>();
    for (const task of proposal.tasks) {
      const signature = [...task.idea_ids].sort().join('\0');
      const bucket = byIdeas.get(signature);
      if (bucket) bucket.push(task.key);
      else byIdeas.set(signature, [task.key]);
    }
    const recorded: Record<string, ShelvedDepRepair> = {};
    const resolvedTargets = new Map<string, string>();
    const remapRepair = (repair: ShelvedDepRepair): ShelvedDepRepair => {
      const entry = copyRepair(repair);
      if (!entry.depIdeas) return entry;
      const pending: Record<string, string[]> = {};
      const mapped = entry.deps.map((dep) => {
        const ids = entry.depIdeas?.[dep];
        if (!ids) return dep;
        if (proposed.has(dep)) {
          pending[dep] = [...ids];
          return dep;
        }
        const candidates = byIdeas.get([...ids].sort().join('\0')) || [];
        if (candidates.length === 1) {
          const successor = candidates[0] as string;
          resolvedTargets.set(dep, successor);
          pending[successor] = [...ids];
          return successor;
        }
        pending[dep] = [...ids];
        return dep;
      });
      const deps = [...new Set(mapped)];
      const next: ShelvedDepRepair = {deps, ideas: [...entry.ideas]};
      if (Object.keys(pending).length > 0) next.depIdeas = pending;
      return next;
    };
    const transfer = (key: string, repair: ShelvedDepRepair) => {
      const current = recorded[key];
      if (!current) {
        recorded[key] = copyRepair(repair);
        return;
      }
      for (const dep of repair.deps) if (!current.deps.includes(dep)) current.deps.push(dep);
      for (const id of repair.ideas) if (!current.ideas.includes(id)) current.ideas.push(id);
      if (repair.depIdeas) {
        current.depIdeas = current.depIdeas || {};
        for (const [dep, ids] of Object.entries(repair.depIdeas)) {
          const bucket = current.depIdeas[dep] || [];
          for (const id of ids) if (!bucket.includes(id)) bucket.push(id);
          current.depIdeas[dep] = bucket;
        }
      }
    };
    const adoptable = (task: IntentTask, repair: ShelvedDepRepair) =>
      [...(task.depends_on || [])].every((dep) => repair.deps.includes(dep)) &&
      repair.deps.some((dep) => shelved.has(dep));
    const liveAdoptable = (task: IntentTask, repair: ShelvedDepRepair) =>
      repair.deps.length > 0 &&
      [...(task.depends_on || [])].every((dep) => repair.deps.includes(dep)) &&
      repair.deps.every((dep) => proposed.has(dep));
    const carriesRepair = (entry: ShelvedDepRepair) =>
      entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0;
    const ownerIdeasProposed = (entry: ShelvedDepRepair) =>
      entry.ideas.length > 0 && entry.ideas.every((id) => ideas.has(id));
    const ambiguousOwners: string[] = [];
    for (const [key, repair] of Object.entries(this.shelvedPrunedDeps)) {
      const entry = remapRepair(repair);
      const current = proposed.get(key);
      if (current) {
        if (adoptable(current, entry)) transfer(key, entry);
        continue;
      }
      if (shelved.has(key)) {
        if (entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0) recorded[key] = entry;
        continue;
      }
      const signature = [...entry.ideas].sort().join('\0');
      const exact = proposal.tasks.filter(
        (task) => task.idea_ids.length === entry.ideas.length && [...task.idea_ids].sort().join('\0') === signature
      );
      if (exact.length === 1) {
        const sole = exact[0] as IntentTask;
        if (adoptable(sole, entry) || liveAdoptable(sole, entry)) transfer(sole.key, entry);
        else if (carriesRepair(entry) && ownerIdeasProposed(entry)) recorded[key] = entry;
        continue;
      }
      if (exact.length > 1) {
        if (entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0) recorded[key] = entry;
        ambiguousOwners.push(key);
        continue;
      }
      if (carriesRepair(entry) && ownerIdeasProposed(entry)) recorded[key] = entry;
    }
    if (resolvedTargets.size > 0)
      this.shelvedTasks = this.shelvedTasks.map((task) => {
        if (!task.depends_on || task.depends_on.length === 0) return task;
        let changed = false;
        const depends_on = task.depends_on.map((dep) => {
          const successor = !shelved.has(dep) && !proposed.has(dep) ? resolvedTargets.get(dep) : undefined;
          if (successor && successor !== dep) {
            changed = true;
            return successor;
          }
          return dep;
        });
        return changed ? {...task, depends_on} : task;
      });
    this.shelvedPrunedDeps = recorded;
    this.pendingDropped = [];
    this.pendingRestored = [];
    return ambiguousOwners.length > 0;
  }

  // Cross-device recovery must preserve dependency repairs already retained locally.
  mergeRepair(owner: string, repair: Required<ShelvedDepRepair>): void {
    const prior = this.shelvedPrunedDeps[owner];
    if (!prior) {
      this.shelvedPrunedDeps[owner] = {
        deps: [...repair.deps],
        ideas: [...repair.ideas],
        ...(Object.keys(repair.depIdeas).length > 0 ? {depIdeas: {...repair.depIdeas}} : {}),
      };
      return;
    }
    for (const dep of repair.deps) if (!prior.deps.includes(dep)) prior.deps.push(dep);
    for (const link of repair.ideas) if (!prior.ideas.includes(link)) prior.ideas.push(link);
    if (Object.keys(repair.depIdeas).length > 0) {
      prior.depIdeas = prior.depIdeas || {};
      for (const [dep, ids] of Object.entries(repair.depIdeas)) {
        const bucket = prior.depIdeas[dep] || [];
        for (const link of ids) if (!bucket.includes(link)) bucket.push(link);
        prior.depIdeas[dep] = bucket;
      }
    }
  }
}

function pendingRepairLineage(repair: ShelvedDepRepair, live: Set<string>, shelved: Set<string>): string[] {
  return Object.keys(repair.depIdeas || {}).filter((dep) => !live.has(dep) && !shelved.has(dep));
}

function copyRepair(repair: ShelvedDepRepair): ShelvedDepRepair {
  return {
    deps: [...repair.deps],
    ideas: [...repair.ideas],
    ...(repair.depIdeas
      ? {depIdeas: Object.fromEntries(Object.entries(repair.depIdeas).map(([dep, ids]) => [dep, [...ids]]))}
      : {}),
  };
}

function lineageForDeps(
  deps: string[],
  lookup: (dep: string) => string[] | undefined,
  prior?: Record<string, string[]>
): Record<string, string[]> {
  const depIdeas: Record<string, string[]> = {};
  if (prior) for (const [dep, ids] of Object.entries(prior)) depIdeas[dep] = [...ids];
  for (const dep of deps) {
    if (depIdeas[dep]) continue;
    const ids = lookup(dep);
    if (ids && ids.length > 0) depIdeas[dep] = [...ids];
  }
  return depIdeas;
}

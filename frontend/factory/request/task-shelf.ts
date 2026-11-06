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

  // Canonical task binding (IntentRevision.Validate rejects declined
  // references in every state): drop tasks linked to declined ideas without
  // re-deciding anything, prune dependency edges left dangling by those
  // drops, and restore shelved tasks whose links are all decided again.
  // Accepted-only subset membership stays out of scope here.
  // Restore-before-prune: the restorable shelved set joins the live set
  // BEFORE any edge is cut, so a kept task depending on re-accepted work
  // keeps its edge and shelved↔shelved edges survive regardless of shelf
  // order. Kept tasks whose edges an earlier decline save cut are repaired
  // first from the recorded pre-prune originals (only edges pointing at
  // live work return); declined work stays declined until re-accepted.
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
    // Shelved originals can themselves be cut (shelved while already
    // pruned by an earlier decline), so they get the same recorded repair.
    // The raw restorable entries travel to the save as pendingRestored: a
    // restored task is absent from the prior revision, so its exact
    // pre-prune edges must be captured from this shape before pruning.
    const restored = restorable.map((task) => this.pruneTaskDeps(this.repairTaskDeps(task, live), live));
    return {tasks: [...pruned, ...restored], dropped, restored: restorable};
  }

  // Re-attach only recorded pre-prune edges that point at live work, in
  // their original order. Edges to still-declined or unknown keys stay cut;
  // no edge is ever invented.
  private repairTaskDeps(task: IntentTask, live: Set<string>): IntentTask {
    const recorded = this.shelvedPrunedDeps[task.key]?.deps;
    if (!recorded || recorded.length === 0) return task;
    const current = new Set(task.depends_on || []);
    // Recorded order is the pre-prune order; stored edges keep their place
    // and only the cut edges return, appended in recorded order.
    const repaired = recorded.filter((dep) => live.has(dep) && !current.has(dep));
    if (!repaired.length) return task;
    return {...task, depends_on: [...(task.depends_on || []), ...repaired]};
  }

  private pruneTaskDeps(task: IntentTask, live: Set<string>): IntentTask {
    const depends = task.depends_on || [];
    const kept = depends.filter((dep) => live.has(dep));
    return kept.length === depends.length ? task : {...task, depends_on: kept};
  }

  // Unresolved repair records: entries whose owner is absent from both the
  // current and shelved sets (an ambiguous split kept the record under the
  // old owner key). Derived, never stored separately: the record itself is
  // the state, and the review below renders while any entry qualifies —
  // across saves, reloads and leaves — until a unique successor claims it
  // or the user confirms the drop. Never silently erased.
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

  // Keep the current save's declined-linked drops for recovery: shelved
  // tasks survive so re-accepting an idea restores them, restored keys leave
  // the shelf, and drops whose ideas are gone (a cleared new request) are
  // forgotten. Nothing here re-decides any idea. Kept tasks whose
  // dependency edges this save cut record their pre-prune originals so a
  // later re-accept re-attaches exactly those edges; restored shelf tasks
  // record theirs from the restored shape (they are absent from the prior
  // revision); entries whose edges no longer point at shelved work and
  // carry no pending lineage are forgotten.
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

  // Pin the stable lineage of shelved tasks' edges: an explore may
  // regenerate live targets under fresh keys, and shelved targets return
  // under their stable keys but may regenerate after that, so the record
  // carries every known target's idea links to remap the edge onto the
  // successor later. No remap fires while the recorded key still resolves.
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
    // Records whose owner is repaired/live or unassigned carry dependency
    // keys reconcile remapped onto successors: refresh their target lineage
    // from the current task graph so a later regeneration can follow every
    // hop. Only known keys gain entries; unknown keys keep their carried
    // lineage for a later regeneration. Nothing here invents an edge.
    for (const [key, repair] of Object.entries(this.shelvedPrunedDeps)) {
      if (repair.deps.length === 0) continue;
      const depIdeas = lineageForDeps(repair.deps, (dep) => known.get(dep), repair.depIdeas);
      if (Object.keys(depIdeas).length === 0) continue;
      this.shelvedPrunedDeps[key] = {deps: [...repair.deps], ideas: [...repair.ideas], depIdeas};
    }
  }

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
    // Carry forward records for work that is still kept or newly shelved:
    // a recorded edge still pointing at shelved work, or a lineage entry
    // whose target has not come back live, must survive later saves — a
    // second decline (or a staggered re-accept) must not forget the first
    // decline's cut edges.
    for (const [key, repair] of Object.entries(this.shelvedPrunedDeps)) {
      if (!saved.has(key) && !shelved.has(key)) {
        // Unresolved owner: the record outlived its work (an ambiguous
        // split kept it under the old owner key, and no unique successor
        // has claimed it yet). Carry it across saves — the post-save draft
        // re-persist keeps it bound to the new head with the shelf — while
        // its lineage still names posted work, so a later regeneration can
        // still resolve it or the user can explicitly confirm the drop.
        // Entries with no edges and no pending lineage carry nothing, and
        // entries whose ideas are gone are forgotten with the shelf. Never
        // silently erased otherwise: the next save (limit-only, leave, or
        // the save before an explore) must not lose the ambiguity.
        if (repair.deps.length === 0 && Object.keys(repair.depIdeas || {}).length === 0) continue;
        if (!repair.ideas.some((id) => posted.has(id))) continue;
        recorded[key] = copyRepair(repair);
        continue;
      }
      // A still-shelved owner keeps its entry while it carries anything:
      // its edges may point at live work a later explore re-keys, and only
      // a restore (or forgotten ideas) ends that. A kept owner keeps its
      // entry while an edge points at shelved work or a lineage target has
      // not come back live.
      if (shelved.has(key)) {
        if (repair.deps.length > 0 || Object.keys(repair.depIdeas || {}).length > 0) recorded[key] = copyRepair(repair);
        continue;
      }
      if (!repair.deps.some((dep) => shelved.has(dep)) && pendingRepairLineage(repair, saved, shelved).length === 0)
        continue;
      recorded[key] = copyRepair(repair);
    }
    for (const task of next.tasks) {
      // A restored shelf task is absent from the prior revision: read its
      // exact pre-prune edges from the restored proposal shape, not from
      // the prior revision (which never carried it).
      const before = original.get(task.key) ?? restored.get(task.key);
      if ((before?.depends_on || []).length === 0 && !recorded[task.key]) continue;
      const initial = before?.depends_on || [];
      const after = new Set(task.depends_on || []);
      const cut = initial.filter((dep) => !after.has(dep));
      // Remember the full pre-prune order only while a cut edge still
      // points at shelved work; a fully restored task needs no record.
      // Accumulate against the exact ORIGINAL edges: a later decline sees
      // an already-pruned `before`, so union it with the carried record —
      // never discard edges recorded for still-declined ideas.
      if (cut.some((dep) => shelved.has(dep))) {
        const prior = recorded[task.key];
        // Pin lineage for every recorded edge with known ideas: a later
        // save or explore may re-key any target, and only pinned lineage
        // lets the remap follow it. While the key still resolves the
        // remap is a no-op, so stable backends see no change.
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

  // After the server replaces the proposal, shelved drops stay only while
  // their ideas still exist and no fresh task supersedes their key: explore
  // regenerates work from the current decisions, so a cleared new request
  // forgets its drops while re-accepted ideas keep theirs for the next save.
  // Repair records survive while their task is still proposed with a stored
  // edge subset of the recorded original (a regenerated task with new edges
  // drops its stale record) and a recorded edge still points at shelved
  // work. When explore regenerated the work under a fresh key, the record
  // follows its work only onto a UNIQUE exact-idea-identical successor, so
  // a later re-accept still restores the cut edges onto the proven task. An
  // ambiguous split (two or more tasks sharing the exact lineage) transfers
  // to NONE and surfaces explicit review instead of guessing. Recorded TARGETS follow their
  // successors the same way, by the stable idea lineage pinned at record
  // time: a unique idea-identical successor remaps the edge, while zero or
  // several successors leave the edge cut (never invented) with the pending
  // lineage kept for a later regeneration to recover.
  reconcile(proposal: IntentRevision): boolean {
    const ideas = new Set(proposal.ideas.map((idea) => idea.id));
    const live = new Set(proposal.tasks.map((task) => task.key));
    this.shelvedTasks = this.shelvedTasks.filter(
      (task) => !live.has(task.key) && task.idea_ids.length > 0 && task.idea_ids.every((id) => ideas.has(id))
    );
    const shelved = new Set(this.shelvedTasks.map((task) => task.key));
    const proposed = new Map(proposal.tasks.map((task) => [task.key, task]));
    // Index fresh keys by stable idea linkage: the same ideas under a new
    // key are the same work regenerated, whatever the key says.
    const byIdeas = new Map<string, string[]>();
    for (const task of proposal.tasks) {
      const signature = [...task.idea_ids].sort().join('\0');
      const bucket = byIdeas.get(signature);
      if (bucket) bucket.push(task.key);
      else byIdeas.set(signature, [task.key]);
    }
    const recorded: Record<string, ShelvedDepRepair> = {};
    const resolvedTargets = new Map<string, string>();
    // Remap a record's targets onto successor keys by stable idea lineage.
    // A target that still resolves under its key keeps its pinned lineage
    // for the next regeneration. A target whose lineage is ambiguous or
    // gone is left untouched: the edge stays cut rather than invented, and
    // pending lineage survives for a later regeneration.
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
          // The lineage follows the successor key: a later regeneration
          // re-keys the successor again, and only the carried mapping lets
          // the next remap follow it. Dropping it here would strand the
          // record on a stale key the next explore cannot resolve.
          pending[successor] = [...ids];
          return successor;
        }
        pending[dep] = [...ids];
        return dep;
      });
      // Two recorded targets with identical lineage resolve onto the same
      // successor: one edge, not a duplicated one.
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
    // A unique successor whose recorded targets are ALL live replays the
    // exact recorded edges on the next save: adopt it onto the successor
    // rather than dropping the record for having nothing left to wait for.
    // The lineage match below is already exact (same idea set); no edge is
    // guessed — every replayed target resolves to live work.
    const liveAdoptable = (task: IntentTask, repair: ShelvedDepRepair) =>
      repair.deps.length > 0 &&
      [...(task.depends_on || [])].every((dep) => repair.deps.includes(dep)) &&
      repair.deps.every((dep) => proposed.has(dep));
    const carriesRepair = (entry: ShelvedDepRepair) =>
      entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0;
    // Owner ideas still proposed: the work may come back, so an unclaimed
    // record waits for a later regeneration or an explicit confirm drop.
    // Ideas gone means the direction left the work behind and the record is
    // forgotten with the shelf.
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
        // The owner stays shelved under its stable key: keep the entry with
        // remapped targets so a later re-accept restores onto the successor
        // work. Entries with no edges and no pending lineage carry nothing.
        if (entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0) recorded[key] = entry;
        continue;
      }
      // Owner lineage must be proven exact, never guessed from a shared
      // idea: the recorded idea links must equal the successor's idea links
      // exactly (same set). A split that shares the lineage across two or
      // more tasks proves neither successor, so the repair transfers to NONE
      // and surfaces explicit review instead of inventing an edge on every
      // twin.
      const signature = [...entry.ideas].sort().join('\0');
      const exact = proposal.tasks.filter(
        (task) => task.idea_ids.length === entry.ideas.length && [...task.idea_ids].sort().join('\0') === signature
      );
      if (exact.length === 1) {
        const sole = exact[0] as IntentTask;
        if (adoptable(sole, entry) || liveAdoptable(sole, entry)) transfer(sole.key, entry);
        // Unique but not adoptable (the successor carries out-of-record
        // edges, or a recorded target is neither live nor shelved): keep the
        // remapped record under the original owner key instead of erasing
        // it — a later regeneration or an explicit confirm drop resolves it.
        else if (carriesRepair(entry) && ownerIdeasProposed(entry)) recorded[key] = entry;
        continue;
      }
      if (exact.length > 1) {
        // Ambiguous split: keep the remapped record under the original owner
        // key so a later regeneration can still recover it, and surface
        // explicit review so the user picks the true successor or re-maps
        // manually. Saves carry the unassigned record (recordPrunedDeps) and
        // the unresolved review keeps it visible until a unique successor
        // claims it or the user confirms the drop. Entries with no edges
        // and no pending lineage carry nothing.
        if (entry.deps.length > 0 || Object.keys(entry.depIdeas || {}).length > 0) recorded[key] = entry;
        ambiguousOwners.push(key);
        continue;
      }
      // No successor carries the recorded lineage: keep the record while
      // its owner ideas are still proposed (a later regeneration may bring
      // the work back); forget it with the shelf once its ideas are gone.
      if (carriesRepair(entry) && ownerIdeasProposed(entry)) recorded[key] = entry;
    }
    // Rewrite shelved tasks' own edges onto resolved successors: the shelf
    // entry keeps its meaning (same lineage, fresh key) so a later
    // re-accept prunes nothing that came back live.
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

  // Union-merge a history-staged repair into the shelf record without
  // overwriting local entries: recorded edges accumulate like the ordinary
  // transfer path, so a local repair for the same owner survives.
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

// Lineage entries whose target is neither live nor shelved: the edge's
// successor has not (or not yet) come back, so the lineage stays pending
// for a later regeneration instead of being forgotten.
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

// Pin stable lineage for every recorded target whose ideas are known: a
// later save or explore may re-key any of them — kept work regenerates
// under fresh keys, and shelved work returns under its stable key but may
// regenerate after that — and only pinned lineage lets the remap follow
// the edge. Retained entries never invent edges: the remap fires only for
// a unique idea-identical successor and stays cut otherwise.
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

import type {ChangedFile, ReviewAnchor, ReviewThread} from '../factory/api/collaboration.js';

const diff = {
  pull: {kind: 'pull' as const, repository: '9007199254740993', external_id: 'opaque-pull', number: '23'},
  base: 'a'.repeat(40),
  head: 'b'.repeat(40),
};
const observation = {state: 'available' as const, revision: 'fixture-1', observed_at: '2026-10-31T12:00:00Z'};
export const renamedFile: ChangedFile = {
  diff,
  old_path: 'src/old.ts',
  new_path: 'src/new.ts',
  change: 'renamed',
  patch_state: 'complete',
  additions: 2,
  deletions: 2,
  patch:
    'diff --git a/src/old.ts b/src/new.ts\nsimilarity index 60%\nrename from src/old.ts\nrename to src/new.ts\n--- a/src/old.ts\n+++ b/src/new.ts\n@@ -1,6 +1,6 @@\n export function greet() {\n-  const first = "before";\n-  const second = "before";\n+  const first = "after";\n+  const second = "after";\n   return first + second;\n }\n \n',
};
const anchor: ReviewAnchor = {
  diff,
  old_path: renamedFile.old_path,
  new_path: renamedFile.new_path,
  side: 'old',
  scope: 'lines',
  lines: {start: 2, end: 3},
};
function thread(id: string, location: ReviewAnchor, body: string): ReviewThread {
  return {
    external_id: id,
    pull: diff.pull,
    observation,
    anchor: location,
    recorded_location: null,
    current_anchor: null,
    outdated: false,
    resolution: 'unresolved',
    capabilities: null,
    comments: {
      observation,
      next: '',
      complete: true,
      items: [
        {
          review_id: 'review-1',
          anchor: location,
          comment: {external_id: 'comment-' + id, observation, author: null, body, created_at: null, updated_at: null},
        },
      ],
    },
  };
}
export const reviewThreads = [
  thread('inline-old', anchor, 'Please retain both old-side requirements. <img src=x onerror=alert(1)>'),
  {
    ...thread(
      'historical',
      {...anchor, diff: {...diff, head: 'c'.repeat(40)}},
      'This comment stays at its original head.'
    ),
    outdated: true,
    current_anchor: anchor,
  },
  {
    ...thread('unobserved-original', anchor, 'Retained conversation with unknown original context.'),
    anchor: null,
    recorded_location: {commit: null, path: 'retained.txt', side: 'new' as const, line: 7},
    outdated: null,
  },
];

const lines = 6000;
export const diffFiles = {
  renamed: renamedFile,
  long: {
    ...renamedFile,
    old_path: 'long.ts',
    new_path: 'long.ts',
    change: 'modified' as const,
    patch:
      `diff --git a/long.ts b/long.ts\n--- a/long.ts\n+++ b/long.ts\n@@ -1,${lines} +1,${lines} @@\n` +
      Array.from({length: lines}, (_, i) => `-export const old${i + 1} = ${i};\n`).join('') +
      Array.from({length: lines}, (_, i) => `+export const next${i + 1} = ${i + 1};\n`).join(''),
    additions: lines,
    deletions: lines,
  },
  added: {
    ...renamedFile,
    old_path: '',
    new_path: 'added.ts',
    change: 'added' as const,
    additions: 2,
    deletions: 0,
    patch:
      'diff --git a/added.ts b/added.ts\nnew file mode 100644\n--- /dev/null\n+++ b/added.ts\n@@ -0,0 +1,2 @@\n+export const added = true;\n+export const next = true;\n',
  },
  deleted: {
    ...renamedFile,
    old_path: 'deleted.ts',
    new_path: '',
    change: 'deleted' as const,
    additions: 0,
    deletions: 2,
    patch:
      'diff --git a/deleted.ts b/deleted.ts\ndeleted file mode 100644\n--- a/deleted.ts\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-export const removed = true;\n-export const next = true;\n',
  },
  binary: {...renamedFile, patch_state: 'binary' as const, patch: null},
  truncated: {...renamedFile, patch_state: 'truncated' as const, patch: null},
  unavailable: {...renamedFile, patch_state: 'unavailable' as const, patch: null},
};
export type DiffFixture = keyof typeof diffFiles;

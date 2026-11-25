# Factory desktop designs

These are exported from the design canvas behind
[frontend-pages-plan.md](../frontend-pages-plan.md), as it stood on 2026-11-25
(canvas version 30, with the Factory logo).
They are a copy and will not update when the canvas changes. The live canvas is
<https://claude.ai/artifact/PqHcR4ENgouTMcfES9bDsC>, which is private to its owner
unless they share it.

- `png/` has each artboard rendered at its design size (1280px wide, apart from
  the 240px sidebar). Open these to look at the designs.
- `source/` has the artboard markup (`*.dc.html`) and the canvas layout
  (`canvas.json`). They need the canvas runtime and the role portraits to render,
  so they will not display correctly when opened directly. Use them to read exact
  spacing, colours and copy. They are mock-ups in a different component format,
  not code to copy into the Lit UI.

Where the plan's recorded decisions differ from an artboard, the plan and the
code win. For example, the allowance request on "Proposed verbs" shows Allow and
Decline buttons, but the implemented card only links to Settings, and
interactive HTML posts stay unavailable.

## 1 · Projects and feed

| Artboard | Image |
|---|---|
| Projects | [png/Projects.png](png/Projects.png) |
| Feed | [png/Main.png](png/Main.png) |

## 2 · Shell and setup

| Artboard | Image |
|---|---|
| First run | [png/FirstRun.png](png/FirstRun.png) |
| Accounts | [png/Accounts.png](png/Accounts.png) |
| Settings | [png/Settings.png](png/Settings.png) |
| Sidebar with project list | [png/Nav.png](png/Nav.png) |
| Jump to (Ctrl K) | [png/Jump.png](png/Jump.png) |
| Factory logo | [png/Brand.png](png/Brand.png) |

## 3 · Request and work

| Artboard | Image |
|---|---|
| Request | [png/Request.png](png/Request.png) |
| Work | [png/Work.png](png/Work.png) |
| Work detail | [png/WorkDetail.png](png/WorkDetail.png) |
| Watch | [png/Watch.png](png/Watch.png) |

## 4 · Issues, pulls and knowledge

| Artboard | Image |
|---|---|
| Issues | [png/Issues.png](png/Issues.png) |
| Issue | [png/Issue.png](png/Issue.png) |
| Pull requests | [png/Pulls.png](png/Pulls.png) |
| Pull request | [png/Pull.png](png/Pull.png) |
| Knowledge | [png/Knowledge.png](png/Knowledge.png) |

## 5 · Post types in code

| Artboard | Image |
|---|---|
| Post anatomy, states and roles | [png/PostsAnatomy.png](png/PostsAnatomy.png) |
| Posts that ask you | [png/PostsAsk.png](png/PostsAsk.png) |
| Posts that show progress | [png/PostsProgress.png](png/PostsProgress.png) |
| Posts about problems | [png/PostsProblems.png](png/PostsProblems.png) |
| Posts with results and evidence | [png/PostsResults.png](png/PostsResults.png) |
| Posts with decisions and knowledge | [png/PostsDecisions.png](png/PostsDecisions.png) |
| Run and plan records | [png/PostsRecords.png](png/PostsRecords.png) |
| Posts that close the loop | [png/PostsLoop.png](png/PostsLoop.png) |

## 6 · Proposed post types

| Artboard | Image |
|---|---|
| Interactive HTML posts | [png/PostsInteractive.png](png/PostsInteractive.png) |
| Proposed verbs | [png/PostsProposed.png](png/PostsProposed.png) |

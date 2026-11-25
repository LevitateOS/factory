# Requests and work

A request describes the outcome you want. Factory turns it into planned work items, codes each one, reviews it independently and merges it under your repository's rules.

## Write a request

Open **Request** in the project. Describe the outcome and the people it is for. The Product Lead explores the request with you: it proposes ideas, asks about consequential assumptions and records your decisions. When the request is accepted, the Architect plans it.

## Follow the work

**Work** shows the request overview and the work plan: each work item, what it depends on and its state. Beside them you see dispatch status and the project's repair funding. Activity lists the issues and pull requests the agents created and the verified merges.

Open a work item for **Work detail**: the journey from planned to merged, the attempts, review findings and corrections, and the checks. Each change goes through:

1. **Plan:** the work item states what it changes and what waits for it.
2. **Code:** a coding agent makes the change in the isolated execution boundary, in small commits.
3. **Review:** a separate agent reviews the exact revision; findings go back to the coding agent for correction, and a fresh review checks the corrected revision.
4. **Check and merge:** required checks run on the reviewed revision, which then merges under your repository's policy, automatically or with your approval.

## Watch a run

**Watch** shows a running agent's output live, read-only. Stopping the view does not stop the run. When the run ends, its output is kept with the work item.

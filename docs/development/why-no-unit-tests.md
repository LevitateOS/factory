# Why unit tests are forbidden

If a gate sent you here, you just wrote (or kept) a unit test. Delete it and
verify through integration or end-to-end tests instead. This file explains
why that is not negotiable on this project. The enforcement mechanics live
in [Banning unit tests](no-unit-tests.md).

## The six reasons

These are the movement's case against unit tests in AI-led codebases, and
this project endorses every one of them:

1. **They lock in mediocre "slop" code.** A unit test pins the current
   implementation in place, so every later refactor or removal must drag
   the test along. The worse the code, the more the tests defend it.
2. **They slow the cycle.** They force agents into extra local work
   (write tests, run tests, fix tests) and inflate CI run times, so each
   iteration costs more wall-clock for less signal.
3. **They waste money.** Every generated test burns tokens on the way in
   and useless CI minutes on every run after. Budget spent proving mocks
   is budget not spent proving the system.
4. **AI-written ones echo the implementation.** A model shown a function
   writes assertions that re-state what the function does. That verifies
   nothing independent — the test and the code share one author and one
   misunderstanding.
5. **They miss what matters.** Unit tests cannot see system-level and
   second-order failures: wiring, config, ordering, real-service behavior.
   E2E matches how the software actually runs; unit tests match how it
   reads.
6. **They bloat and embrittle.** Thousands of lines of test code that break
   on every legitimate refactor, each breakage demanding agent time to
   repair something users never asked for.

## What counts as a unit test

Anything that proves behavior without a real system behind it: pure-function
assertions, fakes, stub servers, stub binaries, DOM shims, recorded
fixtures replayed as truth. Names do not promote a test a tier: a file
called `*journey*` running on stub SSH is a unit test, and a package called
`acceptance` full of fakes is a unit-test package. Classify by behavior.

## The bar instead

Every kept test exercises at least one real system: the real database, a
real browser, real subprocesses, a real build, an installed appliance. If
your change cannot be observed through one of those, either the change is
not observable (reconsider it) or the harness needs a seam (add one, then
test through it).

## Objections, answered once

**"TDD needs unit tests."** Write the integration test first. Same
discipline, real signal.

**"Pure logic has no system to test through."** Then it is covered
transitively by the integration tests of its callers, or it is dead code.
Both outcomes are handled without unit tests.

**"This one mock is faithful."** It still cannot drift with the system it
imitates, so it rots from the day it is written. The kept suites use
scripted fakes only *alongside* real systems, never as the thing under
test.

**"Just this once / just this file."** That is how 600+ unit tests
accumulated the first time. The gates exist because "just this once" does
not scale. No exceptions.

## What to do right now

1. Delete the unit test.
2. Reproduce whatever it checked through a kept integration or e2e suite,
   or add a new integration-tier test and extend the `tools/unitban/`
   allowlist with a comment justifying the tier.
3. Re-run the failing gate and watch it pass.

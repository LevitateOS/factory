# Fountain desktop gateway changes for inspection

These two Git patches contain all changes in the local Fountain gateway branch
relative to baseline `1b523c6e3d7370b4abfd3894f7c6cf9bf1853c03`:

1. `778e08aa98`: OAuth-bound desktop repository enrollment.
2. `1a29a5e86e`: immutable native-profile proof binding.

They are review snapshots of separate Fountain-owned source. They are not a
second defining implementation or part of Factory's engine dependency closure.
No credentials, profiles, build output or runtime state are included.

The patches cover the host SDK/model/migrations, OAuth identity propagation,
HTTPS handlers and existing integration journey. The gateway still admits only
repository reads; central placement, user-facing desktop OAuth/refresh and
privileged operation delegation remain unfinished. Retained evidence and open
gates are documented in the Factory security handoff.

The original source and branch remain available in the Fountain worktree at
`/home/vince/.codex/worktrees/factory-native-executor/factory/.artifacts/fountain-desktop-gateway`,
branch `codex/fountain-desktop-gateway`, HEAD
`1a29a5e86e06e370486afeee689eaaf54ea04b7b`.

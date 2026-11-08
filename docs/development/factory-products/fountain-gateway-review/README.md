# Fountain desktop gateway source references

The defining source is retained in canonical `/home/vince/Projects/forgejo-ext`
on main at merge `2e57559c0e4844baba861a86632d083192537ff3`. Its parents retain
both ownership work (`e88b50dadb91b5ea4bb39dcf80307939d3270fed`) and original
gateway commits, without rebasing or replacing them:

- `778e08aa98329848ddca1c07295120cf5fd082a8`: OAuth-bound repository enrollment.
- `1a29a5e86e06e370486afeee689eaaf54ea04b7b`: immutable native-profile proof.
- Original gateway baseline: `1b523c6e3d7370b4abfd3894f7c6cf9bf1853c03`.

Direct defining owners:

- [Gateway contract](/home/vince/Projects/forgejo-ext/docs/native-desktop-gateway.md)
- [SDK](/home/vince/Projects/forgejo-ext/sdk/desktop_grants.go)
- [Grant model](/home/vince/Projects/forgejo-ext/models/desktopgrant/grant.go)
- [Enrollment migration](/home/vince/Projects/forgejo-ext/models/forgejo_migrations/v17d_add-native-desktop-grant.go)
  and [profile binding migration](/home/vince/Projects/forgejo-ext/models/forgejo_migrations/v17e_bind-native-desktop-proof.go)
- [HTTPS handlers](/home/vince/Projects/forgejo-ext/routers/api/v1/repo/desktop_grants.go)
  and [API routes](/home/vince/Projects/forgejo-ext/routers/api/v1/api.go)
- [OAuth identity](/home/vince/Projects/forgejo-ext/services/auth/method/auth_result_oauth.go)
  and [authentication](/home/vince/Projects/forgejo-ext/services/auth/method/oauth2.go)
- [Existing OAuth/enrollment journey](/home/vince/Projects/forgejo-ext/tests/integration/oauth_test.go)

All ten gateway paths at the merge match original gateway head `1a29a5e86e`
exactly; D6 ownership source remains intact. The existing PostgreSQL
`TestNativeDesktopEnrollment` passed on the combined canonical source:
`/home/vince/Projects/d8-gateway-verified/run.log` (reproduction script
`/home/vince/Projects/d8-gateway-check.sh`; integration binary built with pinned
Go 1.26.10 and `sqlite sqlite_unlock_notify` tags). Reused native TLS/keyring
proof is `/home/vince/tmp/fbound23.CWdTev/result.log`. Fixture accounts are not a
personal login or deployed/installed qualification.

The gateway still admits only repository reads. Central placement, user-facing
desktop OAuth/refresh, privileged operation delegation/journals, candidate
transfer and publication remain unfinished. Old-install migrations,
withdrawal/expiry races, reservation startup concurrency and installed custody
remain unqualified; see the [current security handoff](../daybreak-security-handoff.md).
This Fountain source remains outside Factory's engine dependency closure.

Factory's two redundant review patches were removed only after source retention
and verification. Their complete former contents and original README remain in
exact Factory Git tree
`fe40c5ef51d1f6be0b541efa1a9d8f723d7092da:docs/development/factory-products/fountain-gateway-review/`
(the `0001-feat-native-add-OAuth-bound-desktop-repository-enrol.patch` and
`0002-feat-native-bind-desktop-enrollment-to-profile-proof.patch` entries).
Use `git show COMMIT:path` to inspect either historical file; no second source
copy is maintained here.

The original source branch/worktree is preserved at
`/home/vince/.codex/worktrees/factory-native-executor/factory/.artifacts/fountain-desktop-gateway`,
branch `codex/fountain-desktop-gateway`, HEAD
`1a29a5e86e06e370486afeee689eaaf54ea04b7b`. No worktree, ignored artifact,
profile, keyring or retained evidence was removed by this consolidation.

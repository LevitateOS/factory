# Project domain and wire records

`project` owns the canonical identities, profiles, requests and observations for
Soda Projects. It is an internal Go package shared by storage, product APIs,
the host Unix client and factory coordination. Validation is pure: constructing
these values does not create a container, account, terminal or checkout.

Project users work through Spaces, Join, managed terminals and ordinary SSH.
Start with [working inside a project](../../docs/guides/develop.md) and the
[Project model](../../docs/product/projects.md).

## Consumer entrypoints

Import `github.com/levitateos/sodaos/internal/project` and use its types directly
at the host boundary; their JSON field names are shared wire contracts.

| Types | Meaning |
| --- | --- |
| `Create`, `Profile` | Immutable creation identity and pinned profile. |
| `Account`, `ProjectAccessRequest`, `ProjectAccessStatus` | Bound native member account and privilege observation. |
| `Environment`, `Connection`, `LifecycleState`, `OSObservation` | Observed container, network, host keys, start/stop and mutable OS state. |
| `AccessKeys`, `AccessKeyState` | Revision-bound managed public-key observation or replacement. |
| Preparation and decision records | Approved setup/check inputs, environment grants and readiness. |
| `FactoryRun`, launch/state/export/takeover records | Exact supervised execution and retained candidate/work handoff. |

For a privilege-observation input, callers validate the bound identity before
asking the host client to observe it:

```go
request := project.ProjectAccessRequest{
    Project: "p0123456789abcdef01234567",
    Login: "soda-tester", Identity: 42,
}
err := request.Validate()
```

The example identities are synthetic. Real values come from recorded membership.
`ValidID`, `ValidLogin`, `ValidImageRef` and `ValidContainerID` provide shared
shape checks. `Decode` validates a bounded serialized creation profile;
`Create.Validate` also requires a positive owner and complete profile.

## Effects and constraints

The current profile identity is `rocky-headless`. A creation profile records
image and source revision; an OS observation describes the retained mutable
root. Neither is an arbitrary runtime or image selector.

Preparation and execution requests bind immutable identities, input digests,
fixed roles, approved entrypoints and deadlines. Their validators check shape
and consistency; admission, permissions and native evidence still belong to
the calling boundary. Native effects occur through `host.Client` and its
installed helpers, and persistent row operations belong to `store`.

See [Project OS](../../docs/reference/project-os.md),
[managed terminals](../../docs/reference/terminal.md),
[factory interfaces](../../docs/architecture/factory-interfaces.md), and
[supported platform scope](../../docs/architecture/release.md#architectures).

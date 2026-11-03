# Factory Project client

Forge uses this package to call Factory's private Project coordination endpoint.
`New(socket)` supplies bounded Unix HTTP calls for `StopProject`,
`VerifyProjectStart`, `ClearProjectStopAfterStart` and `EffectiveAuthority`.
Only the admitted service OS UID can connect to the receiving control listener.
Never mount this socket in a Project or expose it through browser HTTP.

The client returns canonical Factory records and `factory.ErrNotFound`. It does
not construct a coordinator, own SQL, deliver credentials or retry mutations.
An unavailable response does not establish whether a native effect completed.
The [Factory service guide](../../../cmd/soda-factory-server/README.md) owns the
process, socket and shared-storage contracts.

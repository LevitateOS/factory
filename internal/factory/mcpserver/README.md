# Factory worker protocol server

`NewHandler(Dependencies, key)` composes the role-filtered MCP SDK server with
the shared `httpapi.Workers` boundary. Hosts supply canonical store/service ports,
current worker admission, runtime observation/task commands and optional provider,
catalogue, evidence, repair and issue-authoring ports. This package imports no
appliance API, host client, session or Forgejo implementation.

The handler authenticates the fixed private `/mcp` route with the supplied key
and recorded capability. Per-request bound identity is private; tool arguments
cannot choose another worker. A single private limiter survives stateless SDK
requests for that handler. Discovery filters by admitted profile and captured
task-command support. The shared wire package owns tool schemas and payload
contracts; shared services own context, verification, consultations, result
binding, terminal checks and task admission. Durable work uses existing store
posts/receipts and workflow consumers.

Construction starts no listener, process, task, provider call or background loop.
Host composition owns key loading, credential delivery, listeners, private socket
permissions and process lifetime. Appliance composition currently lives in
`internal/web/api`; standalone process/configuration composition remains a later
slice. The public constructor does not export an unbound transport or accept
caller-supplied bound identity/limiter state.

The existing catalog integration file moved here with its stdio child and HTTP
checks. Its in-memory journal is a declared protocol fixture. Existing API
PostgreSQL/MCP integrations exercise this same public constructor and shared
services, including native identities without appliance Project inventory; these
checks do not qualify installed native execution.

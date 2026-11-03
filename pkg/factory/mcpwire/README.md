# Factory worker wire contract

`Tool` builds a fresh MCP SDK definition for the existing Factory or System One
tool. Bound definitions omit model-supplied capability identity. Envelopes,
required/optional keys, descriptions, typed role carriers, plan vocabulary,
server-filled fields and bounded cursor/upload/journey/repair shapes have one
owner here. `ResultSchema` supplies the same result carrier to current-role
context. The protocol server still filters discovery by admitted profile and
available execution ports.

`RenderCurrent` supplies the existing compact current metadata plus the full
required-role stream as `ContextPage`/`ContextRecord`. Record fragments preserve
UTF-8 boundaries and order. Cursors bind to that role snapshot; malformed/stale
cursors return no partial view. Collection/record/page limits remain finite,
including `MaxContextPages`; oversized collections are unavailable and hold work.
Missing required inputs keep `CurrentView.Hold` set even when verification is
valid and all transport pages have been collected. Tool guidance uses the same
result schema and omits native task tools when the bound server lacks that port.
Rendering is read-only and authorizes no execution or settlement.

`CanonicalPayload` retains the existing JSON-object and 64 KiB bound.
`CheckPayload` retains per-tool key, required-field and enum admission, including
the existing Issue Maker limits. Typed result decoding, semantic validation,
approved catalogue checks, settlement, authority and durable writes remain with
their existing domain/service/store owners. A schema or valid payload grants no
execution, placement, credential, spending or workflow authority.

This package depends on the existing schema/MCP SDK and domain DTOs/guidance. It
has no appliance API, session, provider client or concrete runtime dependency.
The shared [mcpserver](../mcpserver/README.md) consumes it and routes calls to
existing services and journals. Standalone process composition remains separate.
No alternate wire version, compatibility alias or second workflow journal is
introduced.

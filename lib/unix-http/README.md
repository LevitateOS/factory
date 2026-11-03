# soda-unix-http

Make one bounded HTTP/1 request to an existing Unix socket. Soda's private Rust
clients use this library for HTTP framing, response collection and connection
lifetime management. The package is `soda-unix-http`; Rust imports use
`soda_unix_http`.

## Use

Call the synchronous `request` function with the socket, method, target, Host
header, request body, timeout and response limits:

```rust,no_run
use std::{path::Path, time::Duration};
use soda_unix_http::{request, Limits};

let response = request(
    Path::new("/home/soda-test/service.sock"),
    "POST",
    "/test",
    "soda-test",
    b"{}",
    Duration::from_secs(1),
    Limits { header_bytes: 8192, body_bytes: 4096 },
).expect("private service request");
// Apply the service's status and JSON validation here.
```

The socket and route above are illustrative: choose the endpoint owned by your
service. [`Response`](src/lib.rs) contains `status: u16` and `body: Vec<u8>`.
HTTP error statuses are returned as responses; transport errors use `Error`.

## Behavior and responsibilities

The timeout covers connection, request transmission, response headers and the
complete body. Responses are bounded by `Limits`; failures distinguish connect,
invalid request, protocol, deadline and oversized body errors. Calls do not
retry. Each call sends JSON content type and closes its connection, retiring
the HTTP driver before returning.

The synchronous APIs create their own Tokio runtime. Use them from a
synchronous context or a blocking worker when your caller already runs async
tasks. The caller owns socket access, request authorization, timeout selection,
HTTP status interpretation and domain errors. This crate has no CLI or server.

See the [trust model](../../docs/architecture/trust.md) for authority boundaries
and the [API reference](../../docs/reference/api.md) for existing Soda interfaces.

`request_authenticated` uses the same HTTP driver, deadline and response limits,
adding one Bearer header for trusted native callers. Invalid header values refuse
through the existing `InvalidRequest` outcome. The native supervisor retains its
key on the owned runtime thread; no capability or socket path belongs in browser
state. The helper adds framing, not account/resource authority, retry or token
storage. The ordinary `request` path still sends no authorization header.

`request_authenticated_async` uses the caller's Tokio runtime and awaits the
same bounded HTTP driver. It accepts up to eight sensitive native `X-Soda-`
headers, without allowing replacement of framing or authorization headers.
Dropping its future aborts the owned driver and closes the Unix connection;
the remote Go request can observe cancellation rather than outlive the caller.
Sensitive header values are marked for redacted debug formatting. Host-specific
credential scope, response validation and post-I/O admission remain with callers.

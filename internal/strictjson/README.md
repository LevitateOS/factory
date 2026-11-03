# Strict JSON request decoding

Go handlers in Soda use `strictjson.Decode` to read a bounded single JSON object
before applying their own domain validation. This package owns JSON admission;
it does not authorize a request or decide product behavior.

## Use inside the Soda module

Decode into a typed destination with explicit JSON fields:

```go
import (
    "strings"
    "github.com/levitateos/sodaos/internal/strictjson"
)

var request struct {
    Name string `json:"name"`
}
err := strictjson.Decode(strings.NewReader(`{"name":"example"}`), &request)
```

Check `err` before using `request`, then validate the decoded values and caller
authority. [`Decode`](decode.go) takes an `io.Reader` and a destination pointer.

## Admission behavior

Inputs must contain exactly one object encoded as valid UTF-8 and fit within
1 MiB. Duplicate field names are refused throughout nested objects, including
objects in arrays. Excessive nesting, trailing JSON and incomplete input are
refused. Unknown fields are rejected when decoding typed structs.

The function reads the bounded body before decoding and never logs its contents.
Its errors report read or decoding failures. Callers choose HTTP status and
domain errors; they also provide transport deadlines, since a byte limit does
not stop a reader from blocking.

See [Go package conventions](../../docs/development/go-packages.md) for consumer
boundaries and the [API reference](../../docs/reference/api.md) for Soda's actual
request interfaces.

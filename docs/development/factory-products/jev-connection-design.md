# Connecting JEV: backend and wiring design

Status: agreed design, 2026-11-25. No code has been written for it yet. The
screens are on page "7 · JEV" of the design canvas
(<https://claude.ai/artifact/PqHcR4ENgouTMcfES9bDsC#page-jev>, private to its
owner). The design was agreed on 2026-11-25; the slices are in [implementation-tasks.md](implementation-tasks.md#jev-connection-2026-11-25).

## What has to be true

1. The user pastes a TypeSafe API key once, in first run or in Accounts.
   Factory checks it with TypeSafe, keeps it in the macOS Keychain without
   any Keychain dialog, and afterwards shows only its last four characters.
2. Everything that waits on `jev` today gets a real JEV answer. Otherwise it
   shows one clear reason: no key, key refused, TypeSafe busy, offline, or
   request refused.
3. Every decision can show the question, the answer probabilities, the rule
   Factory applied, the versioned model, tokens and cost.
4. A decision costs one paid request. Nothing asks TypeSafe again in a loop.
5. The key never reaches Go, PostgreSQL, logs, Muse workers or the product UI.

## Where each piece lives

```
Shell UI (frontend/desktop)            Product UI (frontend/factory)
  accounts.ts, first-run.ts              feed, settings, sidebar, jump
        │ core.invoke("jev_*")                 │ product API
        ▼                                      ▼
soda-desktop (cmd/soda-desktop/src/jev.rs)   Go server (pkg/factory/runtime/native_jev.go)
        │                                      │ nativeHostCall /api/native/execution
        │                                      │   {operation: "inspect_jev" | "jev_ask"}
        ▼                                      ▼
desktop-runtime JEV connector (lib/desktop-runtime/src/jev_provider.rs)
        │ credentials (Provider::Typesafe, Keychain, no UI)
        ▼
https://api.typesafe.ai  (GET /v1/models, POST /v1/systemone)
```

These follow `language-allocation.md`:

- **Rust** owns key custody, the HTTPS connection to TypeSafe, sorting
  TypeSafe's errors into classes, the per-Mac rate gate, the account's
  default model and the connection status.
- **Go** owns the questions and state, the rules that turn answers into
  actions, durable pending and retry state, receipts in PostgreSQL, usage
  totals, stall reasons and the per-project model pin.
- **Lit** owns the screens.

### The existing Go TypeSafe client

`pkg/factory/control/typesafe_transport.go`, `priority_typesafe.go`,
`systemone_typesafe.go` and `delivery_chooser.go` read a key from a file on
disk and post to TypeSafe from Go. Nothing outside `control` calls them
today. The design keeps one TypeSafe transport (Rust) and one answer
validator (Go):

- **Keep** the answer checks: `ValidateSystemOneAnswer`,
  `validateSystemOneEnvelope`, `systemOneDistribution`, the priority and
  delivery decoding. Change them to take a decoded reply body instead of an
  `*http.Response`.
- **Delete** `postSystemOne`, `postPriorityRequest`, `readPriorityKeyFile`,
  `TypeSafeKeyReadable` and the `APIKeyFile` and `Client` fields.

Fountain is not affected, because nothing in it calls this code.

## Rust: key custody and the connector

### Custody

- `credentials::Provider` gains `Typesafe` (`"typesafe"`). There is one
  account per Mac profile:
  - origin `https://api.typesafe.ai`
  - account `default`
  - label: the key's last four characters
- It uses the existing `keyring.rs` path. That path turns user interaction
  off (`SecKeychainSetUserInteractionAllowed(0)`) and scopes the item to the
  app and profile, so the Keychain never shows a dialog. An item Factory may
  not read counts as "not connected", never as a prompt.
- A non-secret record sits next to it in the profile (`private_fs`). It holds:
  - `added_at` and `checked_at`
  - `last4`
  - `default_model`: `jev-latest` or a pinned version
  - `state`: `connected`, or `refused` with the time it was refused
  - the model list from the last check

### Operations

- **verify(key)** sends `GET /v1/models` with `Authorization: Bearer`.
  - 200: store the key and the record.
  - 401: report "not accepted" and store nothing.
  - Network failure: report "can't reach TypeSafe".
  - Anything else: report the status and message.
- **Replace** verifies the new key first and swaps only if it passes. The old
  key stays in use until then.
- **check** runs `GET /v1/models` with the saved key. It runs only when the
  user presses "Check now" or replaces the key, never on a timer.
- **ask(model, state, questions)** sends `POST /v1/systemone` with a 30 s
  timeout. On success it returns `{model, answers, usage}` exactly as
  TypeSafe sent it. It never sends the long-context header; see the open
  questions.
- **remove** deletes the Keychain item and the record.

Logs carry only the outcome class, HTTP status, model and token counts. They
never carry the key, state or answers.

### Error classes

| TypeSafe reply | Class | Connector | Product shows |
|---|---|---|---|
| 200 | `answered` | returns the reply | the decision |
| 401 | `key_refused` | marks the key refused and refuses later asks without calling TypeSafe until the key is replaced or checked again | Needs you: replace the key; red sidebar line |
| 429, 529 | `busy` | closes the gate until `retry-after`, or 15 s doubling to at most 10 min | Waiting, starts by itself |
| connect, TLS or timeout failure | `offline` | closes the gate for 60 s | Waiting, starts by itself |
| 400 `max_tokens_exceeded`, 413 | `too_large` | no retry | Problem: pick who reads it, or edit the post |
| 415, 422 | `rejected` | no retry; records the `detail` (this is Factory's bug) | Problem with the code |
| anything else (402, 403, 5xx without a class) | `unexpected` | no retry | Problem with the code and message |

The gate is per Mac, because TypeSafe sets rate limits per organization.
While the gate is closed, `jev_ask` returns at once with `busy` or `offline`
and `retry_at`, without calling TypeSafe. Rust holds no queue. Go keeps every
pending decision and its single retry durably, so there is one owner of
waiting work.

## Shell UI to Rust (IPC commands)

The shell UI uses the same pattern as `github_sign_in_*` in `signin.rs`.

- `jev_status` returns the status, never the key:

  ```
  {state: none | connected | refused, last4, added_at, checked_at, refused_at?,
   default_model, models: [{id, alias_of?}], problem?: {class: busy | offline, since, retry_at}}
  ```

- `jev_connect {key}` returns the status, or
  `{error: not_accepted | offline | unexpected, status?, detail?}`. The key
  exists only in this call's payload, and Rust zeroizes it after storing.
- `jev_check` returns the status.
- `jev_set_model {model}` returns the status.
- `jev_remove` returns the status.

The Accounts card's "This month" figure comes from Go (`GET /api/jev/usage`,
summed over all projects) through the product transport the shell already
uses.

## Go to Rust (native host operations)

Both go over `nativeHostCall(..., "/api/native/jev", …)`. That is a separate
route from `/api/native/execution`, because an ask may take TypeSafe's whole
30 s timeout and the execution route allows 5–8 s.

- `inspect_jev` returns the `jev_status` body. It is local and costs no
  TypeSafe request. Go uses it for the sidebar, Settings and stall reasons.
- `jev_ask {model?, state, questions}` returns the following. `reply` is
  TypeSafe's whole answer body, and Go checks it with the kept validators.

  ```
  {outcome: answered | key_refused | not_connected | busy | offline | too_large | rejected | unexpected,
   retry_at?, http_status?, detail?, reply?}
  ```

## Go: one path for every decision

One owner, `pkg/factory/runtime/native_jev.go`, implements every port
through one function:

```
ask(ctx, decision{purpose JudgmentPurpose, repository, subject, state, questions, rule})
```

1. **Model.** Use the project pin if there is one, else the account default
   from `inspect_jev`, else `jev-latest`.
2. **Journal.** Write a row in the existing command journal, keyed by
   purpose, subject and a digest of state, questions and model.
   - If an answered row exists, Factory reuses it. A paid answer is a fact,
     never asked for again.
   - If a pending row's `retry_at` is still in the future, return
     `ErrJudgmentWaiting` with the stall reason.
3. **Ask.** Call `jev_ask`.
   - `answered`: check the answer with the kept validators and store the
     receipt. Then apply the rule in code, which gives the action, "ask the
     user" or "use the fallback".
   - `busy` or `offline`: store `retry_at`. One scheduler retries due
     decisions, oldest first and one at a time while the gate is closed.
   - `not_connected` or `key_refused`: store the stall reason. Everything
     resumes, oldest first, when `inspect_jev` reports `connected`. Rust
     tells Go after `jev_connect`, `jev_check` or a replace, and Go does not
     poll TypeSafe.
   - `too_large`, `rejected` or `unexpected`: post a Problem with the code.
     Never retry.
4. **Crash with a request in flight.** TypeSafe has no idempotency key, so
   a restart sends the request once more. The receipt records the attempt
   count.

All question wording lives in one file (`jev_questions.go`). The
thresholds stay with the shared contracts in `pkg/factory/judgment.go`, which
already holds the delivery thresholds. The starting thresholds are 0.70 for
yes and 0.30 for no, and the band between them means unsure.

### Ports and their questions

| Port (unwired today) | Questions sent | Rule in code | When unsure |
|---|---|---|---|
| PostJudge (Feed routing) | Choice "Which agent should read this post?" over the enabled roles and their descriptions, plus Noul "Does this post ask any agent to do something?" | Hand off when the top choice is ≥ 0.70 and the Noul is ≥ 0.70 | The post asks you who should take it |
| Priority (next work) | Choice over the candidates in the existing `PriorityRequest` | Start the top choice | Keep the order you set |
| Planning review | Noul "Does the plan cover everything the request asks for?" | ≥ 0.70 starts the plan; ≤ 0.30 asks for a new plan | The post asks you; your answer is recorded as the plan's approval, replacing the `ErrJudgmentUnavailable` refusal in `checkIssuePlanReviewTx` |
| ContextChooser and CurrentChooser | One Noul per candidate file or note: "Does the agent need this to do the task?" | Include ≥ 0.30 (generous) | Give the wider set |
| Contribution judgments | Existing contract questions, as Nouls | As the existing contract | Ask you |
| Delivery | Existing `SelectDelivery` choice | Existing delivery thresholds | Ask you before delivering |
| SystemOneForWorker (worker MCP) | The worker's own questions, checked by the kept validators and charged to the run | None; the worker reads the answers | Not applicable |

## Status and stall reasons

The single `stalled_jev` reason becomes five:

- `jev_not_connected`
- `jev_key_refused`
- `jev_busy`
- `jev_offline`
- `jev_request_refused`

The Feed renders them as the posts on the "Feed: JEV posts and states"
artboard. `repository_status.go` gains `jev {state, waiting}`, and the
sidebar, project list and quick jump read it from there. Busy and offline
post to the Feed only after the first quiet retry fails.

## Usage and cost

- Go stores one receipt per answered ask: purpose, repository, subject,
  versioned model, input and output tokens, attempt and time. The existing
  charge records get first refusal; a separate table is added only if they
  cannot hold this.
- Settings ("this project, this month") and Accounts ("this month") sum
  these receipts.
- Cost is computed when it is read, as input tokens × one price constant
  ($0.042 per million tokens). The screens label it as an estimate, because
  TypeSafe's bill is the final word.

## What is sent to TypeSafe

Factory sends:

- post titles and text
- role names and descriptions
- candidate titles
- request and plan text
- the file paths and short excerpts Factory chose

Factory never sends credentials, GitHub tokens or whole repositories.
TypeSafe states that it does not train on customer requests. Accounts lists
this under "What Factory sends to TypeSafe".

## How it will be verified

The plan will spell these out.

- **Controlled TypeSafe replies.** The existing native check gets a base-URL
  override for 200, 401, 429 with `retry-after`, 529, offline, 400 and 422.
  The check proves:
  - one request per decision
  - no retry on 4xx
  - the gate is respected
  - nothing calls TypeSafe while the key is refused
  - the key never appears in logs or in PostgreSQL
- **One connected run with your key, only after asking you:** connect, route
  one post, check its receipt, then remove the key.
- **Signed build, no Keychain dialog:** connect, relaunch, check now, then
  remove.

## Open questions

1. **When JEV is sure, work starts immediately**, and the post shows
   "Decided" with "Wrong call? Choose where it goes". The author can pick
   another option JEV weighed until the routed work starts. Agreed.
2. **One TypeSafe key per Mac**, shared by all projects, with an optional
   model pin per project. Is that enough?
3. **The long-context header stays off.** A post too large for 64k tokens
   becomes a Problem and is not shortened in secret. Should Factory send the
   header instead?

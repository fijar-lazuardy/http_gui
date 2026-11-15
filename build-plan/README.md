# Build plan

A Postman alternative with a native Go GUI, built on [MyGo](https://mygo.run/llms.txt)
native UI (`github.com/egoist/mygo/ui`). No webview, no cgo.

## Scope (v1)

1. HTTP requests with the standard methods (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS).
2. Request body: JSON, multipart form, URL-encoded form.
3. Headers edited in a key/value form.
4. Variables (global, collection, environment, runtime).
5. Pre-request and post-response scripts in JS/TS, with crypto (hash, HMAC, RSA/ECDSA/Ed25519 signing).
6. `{{name}}` substitution in URL, params, headers and body.
7. Import of Postman and Bruno collections.

Out of scope for v1: auth helpers (OAuth etc.), GraphQL/gRPC/WebSocket, cookie jar UI,
collection runner, cloud sync, code generation, Go-language scripts.

## Decisions

| Topic | Decision | Why |
| --- | --- | --- |
| GUI | MyGo native UI, `mygo init -template native` | What was asked; pure Go, instant start, views testable without a window |
| HTTP | `net/http` from the standard library | Nothing to add |
| Scripting | [goja](https://github.com/dop251/goja) (pure-Go JS engine) + `goja_nodejs` (`require`, `console`, `Buffer`) | No cgo, matches MyGo; widely used (k6, Grafana) |
| TypeScript | esbuild's Go API (`github.com/evanw/esbuild/pkg/api`, `Transform`) strips types before goja runs the script | Pure Go, no Node or Bun needed |
| Script API | Postman's `pm.*` (subset) as the primary API, plus a thin `bru`/`req`/`res` shim | Imported scripts run unchanged; no new API to learn |
| Crypto in scripts | Node-shaped `require('crypto')` backed by Go's `crypto/*`, plus embedded `crypto-js` as the `CryptoJS` global | Go stdlib does the real work; `CryptoJS` is what Postman scripts already call |
| Storage | Plain JSON files in a workspace folder, one file per request | Git-friendly, `encoding/json` only |
| Variables | Postman's `{{name}}` syntax and precedence | Imports work as they are |

## Architecture

The engine has no UI imports, so every package below `internal/` except `internal/app`
is tested with plain `go test`.

```
main.go               window + wiring
internal/model        Collection, Folder, Request, Body, KV, Environment
internal/store        load/save the workspace on disk
internal/vars         {{name}} resolver, scopes, dynamic variables
internal/httpx        model.Request -> *http.Request, send, Response + timings
internal/script       goja runtime, pm/bru API, crypto module, TS transform
internal/importer     postman/, bruno/ -> model
internal/runner       one run: vars -> pre-script -> resolve -> send -> post-script
internal/app          state + views (the only package importing mygo/ui)
```

A run, in order (`internal/runner`):

1. Snapshot the variable scopes.
2. Run the pre-request script (may change variables and the request).
3. Resolve `{{name}}` everywhere in the request.
4. Send, with a cancellable context.
5. Run the post-response script (reads the response, may change variables, records tests).
6. Persist changed variables, return the response, console output and test results.

Runs happen in a goroutine; the result reaches the view through `Window.Update`.

## Steps

Each step ends in something that works and is tested. Do them in order; 02-04 and 07-08
need no window.

| # | File | Result |
| --- | --- | --- |
| 00 | [00-prerequisites-and-scaffold.md](00-prerequisites-and-scaffold.md) | An empty MyGo window builds and its test passes |
| 01 | [01-model-and-storage.md](01-model-and-storage.md) | Collections and environments round-trip to disk |
| 02 | [02-http-engine.md](02-http-engine.md) | Requests with all three body kinds are sent from Go |
| 03 | [03-variables.md](03-variables.md) | `{{name}}` resolves across the request |
| 04 | [04-scripting.md](04-scripting.md) | Pre/post scripts in JS/TS with crypto |
| 05 | [05-ui-shell.md](05-ui-shell.md) | Sidebar tree, environment picker, request pane |
| 06 | [06-request-and-response-ui.md](06-request-and-response-ui.md) | Send a request from the window and read the response |
| 07 | [07-import-postman.md](07-import-postman.md) | Postman collections and environments import |
| 08 | [08-import-bruno.md](08-import-bruno.md) | Bruno collections import |
| 09 | [09-polish-and-packaging.md](09-polish-and-packaging.md) | Request tabs, shortcuts, packaged builds |

## Status

| Step | State |
| --- | --- |
| 00-03 | Done: `go test ./...`, `go vet ./...` and `go tool mygo vet .` pass. The window itself has not been opened yet (step 00 was built in a shell without a display). |
| 04-09 | Not started |

## Settled

1. **Names.** Module `http_gui`, app "HTTP GUI". The folder is still `http_tui` (a typo for
   gui); renaming it is the owner's call. `mygo.json` has the placeholder identifier
   `com.example.httpgui`, to set before packaging (step 09).
2. **Scripts are JS and TS only.** No Go-language scripts.
3. **Scripts are inline** strings in the request's JSON, as Postman keeps them.

## Open questions

1. **Secret variables.** Now: a `secret` flag that masks the value in the UI, stored in the
   same JSON. `mygo.Secrets` (system keychain) is the upgrade. Decide before step 05.

## Things to verify while building

These come from docs or memory, not from running code:

- `ui.Split` is documented as side by side; a top/bottom split for request over response
  may need a `Column` with a draggable divider instead (step 06).
- goja's async/await and Promise settling need an event loop (`goja_nodejs/eventloop`)
  for scripts that `await` (step 04).
- `goja_nodejs` has no `crypto` module of its own; step 04 writes it.
- Bruno's current on-disk format (classic `.bru` vs. the newer YAML one) at import time
  (step 08).

# AGENTS.md

A Postman alternative with a native Go GUI: send HTTP requests, edit headers and bodies,
use variables with `{{name}}`, run pre-request and post-response scripts, and import Postman
and Bruno collections.

**Status:** steps 00-08 of the plan are done: the scaffold, the model and its storage, the
HTTP engine, variables, scripts, the sidebar, the request and response panes and the
Postman and Bruno imports. Polish and packaging are not. The plan is in
[`build-plan/`](build-plan/README.md), with a status table; do its steps in order.

The module is `http_gui` and the app is "HTTP GUI". The folder's name, `http_tui`, is a typo.

## Stack

- **Go 1.27.3+**, one module, no cgo. With gvm the default `go` may be older: check
  `go version` and use `~/.gvm/gos/go1.27.3/bin/go` (with `GOROOT` set to match) if it is.
- **GUI:** [MyGo](https://mygo.run/llms.txt) native UI, `github.com/egoist/mygo` and its
  `ui` package. No webview, no HTML, no JavaScript frontend.
- **HTTP:** `net/http`.
- **Scripts:** `github.com/dop251/goja` with `goja_nodejs`; TypeScript through
  `github.com/evanw/esbuild/pkg/api`.
- **Storage:** JSON files in a workspace folder, `encoding/json`.

Add no other dependency without asking.

## Layout

```
main.go               window + wiring
internal/model        Collection, Folder, Request, Body, KV, Environment
internal/store        load/save the workspace on disk
internal/vars         {{name}} resolver and scopes
internal/httpx        build and send requests
internal/script       goja runtime, pm/bru API, crypto module
internal/importer     postman/, bruno/
internal/runner       one run: pre-script -> resolve -> send -> post-script
internal/app          state and views
build-plan/           the plan, one file per step
```

Only `internal/app` and `main.go` import `mygo` or `mygo/ui`. Everything else must be
testable without a window.

## Commands

```sh
go tool mygo dev      # run with live reload
go test ./...         # all tests, views included (no window needed)
go vet ./...
go tool mygo vet .    # finds build values stored where they must not be
go tool mygo build    # package for this platform
go tool mygo doctor   # check the machine
```

## MyGo rules

MyGo is immediate mode: the view is a function from the app's state to the interface, and
it runs for every frame. Read the page for a widget before using it; the index is
<https://mygo.run/llms.txt> and each page is Markdown at `https://mygo.egoist.dev/docs/...md`.
`.agents/skills/mygo-maintenance/SKILL.md` has MyGo's own guidance; follow it.

- State lives in the `App` struct, made once at start. Create nothing lasting inside the
  view (no routers, buffers or `editor.State` values).
- Never store a `ui.Element`, or anything else from a build pass, in a struct, a package
  variable or a goroutine. `go tool mygo vet` checks this.
- Widgets take pointers to state (`ui.TextInput(c, &a.url)`) and write the edit before
  returning. Ask `Changed()`, `Clicked()`, `Submitted()` for what follows.
- Use `OnClick`/`OnChange` instead of `Clicked`/`Changed` when the action changes the
  collection the loop is building.
- Give every element built in a loop a stable key first: `c.Key(item.ID)`. Use IDs, not
  indexes.
- The view runs on the main thread. A goroutine changes state only inside
  `win.Update(func() { ... })`. Network calls and scripts never run in the view.
- Text and code editing: `ui.TextInput` for one line, `editor.View` from `ui/editor` for
  bodies, scripts and responses.

## Testing

- Engine packages: table tests, `httptest.Server` for anything HTTP, `t.TempDir()` for
  anything on disk. No test reaches the network.
- Views: `ui.NewTester(app.View, w, h)`, then `Click`, `Type`, `Key`, `HasText`. Advance the
  tester's clock with `tt.Advance`; never `time.Sleep`.
- A test says why the behavior matters (a signature the server accepts, a variable that
  resolves), not only that a function returns a value.
- Script and crypto tests check against Go's own `crypto` results or published vectors.

## Conventions

- Smallest change that does the step. No abstractions for one caller, no features the plan
  does not list.
- Errors are returned and shown, never swallowed. Importers return warnings for everything
  they could not map.
- Scripts are untrusted input from imported files: they get no file system or network
  access, and every run has a deadline. Anything added to the script API keeps that:
  `TestScriptsReachNothing` in `internal/script` must go on passing.
- The script API's shape is in `internal/script/js/prelude.js`; Go provides the functions
  under it (`host` in `script.go`) and `require('crypto')` (`crypto.go`). Third-party JS is
  embedded from `internal/script/js/`, with its license and checksum in the README there.
- The on-disk format in `build-plan/01-model-and-storage.md` is a contract once users have
  workspaces; change it only with a migration.
- When the plan and the code disagree, say so and update the plan in the same change.

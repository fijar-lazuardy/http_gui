# 06 - Request and response UI

**Result:** the app is usable end to end: pick a request, edit it, press Send, read the response.

## Layout

```
[ POST v ] [ {{url}}/login                                ] [ Send ]
[ Params | Headers | Body | Scripts ]
  ...the chosen tab...
---------------------------------------------------------------------
200 OK   143 ms   1.2 KB
[ Body | Headers | Console | Tests ]
  ...the chosen tab...
```

## Request side

- **Request line:** `ui.Select` of methods, `ui.TextInput` for the URL (`Submitted` sends),
  `ui.PrimaryButton` "Send", which reads "Cancel" while a run is in flight.
- **Params / Headers:** the key/value editor from step 05. Params and the URL's query string
  stay in sync in both directions. On disk the request's `url` has no query: the URL field
  shows `url` with the enabled params after it, unescaped, and typing a query in it moves
  it into `params` (httpx appends the params to the URL, so keeping both would send the
  query twice). Rows switched off stay in Params only.
- **Body:** `ui.Segmented` of None / JSON / Form / URL-encoded.
  - JSON: `editor.View` from `ui/editor` with the JSON language (`editor.LanguageNamed`),
    one `editor.State` per open request kept in `App`. A "Format" button pretty-prints valid
    JSON; invalid JSON shows a quiet warning and can still be sent.
  - Form (multipart): key/value editor with a per-row Text/File choice; File opens
    `mygo.Dialog.Open`.
  - URL-encoded: key/value editor.
- **Scripts:** `ui.Segmented` Pre-request / Post-response, a JS/TS `ui.Select`, and an
  `editor.View` with the matching language.
- **Unresolved variables:** names from `vars.Resolve`'s `missing` are listed under the
  request line before sending, so a typo in `{{name}}` is visible.

## Response side

- **Status line:** status (colored by class), duration, size. While sending: `ui.Spinner`.
  On a transport error: the error text, nothing else.
- **Body:** read-only `editor.View` (`ReadOnly`), language picked from the response
  `Content-Type`; JSON is pretty-printed for display, with a Raw toggle. Large bodies are
  fine: the editor only lays out the lines in view.
- **Headers:** `ui.Table`, name and value.
- **Console:** `console.log` output and script errors from the run.
- **Tests:** `pm.test` results, passed and failed counts in the tab label.

## Sending

```go
func (a *App) send() {
    ctx, cancel := context.WithCancel(context.Background())
    a.cancel, a.sending = cancel, true
    in := a.runInput()                 // copies of the request and scopes, made on the main thread
    go func() {
        res := runner.Run(ctx, in)
        a.win.Update(func() { a.finish(res) })
    }()
}
```

The goroutine only touches its copies. `finish` stores the result, persists changed
variables and resets the editor buffers that show the response.

Each run has a number: Cancel, a new Send or opening another request drops the result of
the run in flight, variables included. Redirects are followed and there is no timeout
other than Cancel, until the settings of step 09.

## Split between request and response

`ui.SplitVerticalPercent`, 50/50 at first; both panes grow with the window.

## Tests (`ui.NewTester` + `httptest.Server`)

- Type a URL, click Send: the status and body text appear. The test's `update` sends the
  run's result to a channel; the test runs it from there, then calls `tt.Frame()`; it never
  sleeps.
- Changing the method select sends that method.
- A header added in the editor reaches the server.
- Each body kind entered in the UI reaches the server decoded as entered.
- `{{url}}` in the URL field with an environment chosen hits the server; with none chosen
  the missing name is shown.
- Cancel during a slow handler returns the UI to idle with no response shown.
- A failing `pm.test` shows in the Tests tab.

## Done when

The tests pass, and by hand: a JSON POST with a header, a variable and an HMAC pre-request
script against a public echo service shows the expected response.

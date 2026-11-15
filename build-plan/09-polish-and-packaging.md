# 09 - Polish and packaging

**Result:** the app is comfortable for daily use and ships as installable builds.

Each item is independent; do them in this order and stop wherever v1 is good enough.

## Daily use

1. **Request tabs.** Several requests open at once in a tab row above the request pane, each
   with its own editor state and last response. Unsaved-change dot, middle-click or a close
   button to close.
2. **Shortcuts** (the Input guide's Shortcuts): send (Ctrl/Cmd+Enter), new request, close
   tab, focus URL, switch environment, find in response (`ui.FindBar` over the editor).
3. **Menus.** An application menu with the same commands (`mygo.MenuItem` templates).
4. **Window state.** `StateKey` on the window; sidebar width, open folders, last environment
   and open tabs restored on start.
5. **Settings.** Timeout, follow redirects, TLS verification, script timeout, workspace
   folder. A `ui.Modal`, stored in a small JSON file in the user-data directory.
6. **Response extras.** Copy body, save body to a file (`mygo.Dialog.Save`), image preview
   for `image/*` (`ui.Image`), a size cap above which the body is offered as a download only.
7. **Variable hints.** `{{name}}` highlighted in the URL and editors, with the resolved value
   in a tooltip, and missing names in the theme's warning color.
8. **Theme.** Follow the desktop's light and dark; check every view in both with
   `tt.SetDark`.

## Later candidates (not v1)

Auth helpers (basic, bearer, OAuth 2), cookie jar, request history, collection runner,
`pm.sendRequest`, export to Postman/Bruno, cURL import and export, secrets in the system
keychain (`mygo.Secrets`), collection- and folder-level scripts, Go-language scripts.

## Packaging

- Fill in `mygo.json`: name, identifier, version, icon (`resources/icon.png`, 1024x1024).
- `go tool mygo build` for the local platform; `-platform darwin/universal,windows/amd64,linux/amd64`
  cross-builds everything from one machine, since there is no cgo.
- Signing, notarization and auto-updates are in MyGo's distribution and updater docs; decide
  when there are users other than the author.
- A GitHub Actions workflow that runs `go vet`, `go tool mygo vet` and `go test ./...` on push.

## Done when

`go tool mygo build` produces a package that installs and runs on a clean machine of the
local platform, and the full test suite passes.

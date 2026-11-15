# 00 - Prerequisites and scaffold

**Result:** an empty MyGo native window builds, runs and has a passing view test.

## Prerequisites

- **Go 1.27.3 or later.** MyGo v0.9.0 declares `go 1.27.3`. This machine has Go 1.26.10
  (gvm). Either install 1.27 with gvm, or let Go fetch the toolchain itself
  (`GOTOOLCHAIN=auto`, the default).
- **GTK 3 runtime** on Linux (`libgtk-3-0`). `pkg-config gtk+-3.0` finds nothing here, which
  only says the `-dev` package is absent; MyGo loads the library at run time without cgo.
- `go run github.com/egoist/mygo/cmd/mygo@latest doctor` checks both.

## As built

- Module `http_gui`, app "HTTP GUI", MyGo v0.9.0, Go 1.27.3 (gvm: `~/.gvm/gos/go1.27.3`).
- `mygo doctor`: Go and GTK 3 found. WebKitGTK is reported missing, which only windows
  showing web pages need; this app has none.
- Task 4 was dropped: packages are created by the step that fills them, not as empty
  directories.
- Not verified: `go tool mygo dev` opening the window. The shell this was built in has no
  display. Run it once by hand.

## Tasks

1. Settle the app name and module path.
2. Scaffold with the native template. `mygo init` wants a new or empty directory and this
   repo already has `build-plan/` and `AGENTS.md`, so scaffold beside it and move the files in:

   ```sh
   go run github.com/egoist/mygo/cmd/mygo@latest init -template native -name "<App Name>" -module <module> /tmp/scaffold
   # then move main.go, main_test.go, go.mod, go.sum, mygo.json, resources/, .agents/, .gitignore into the repo
   ```

   This gives `main.go`, `main_test.go`, `go.mod` (with the CLI as a tool), `mygo.json`,
   `resources/icon.png` and `.agents/skills/mygo-maintenance/`.
3. Read `.agents/skills/mygo-maintenance/SKILL.md` and link it from `AGENTS.md`.
4. Create the empty package directories from the README's architecture, each with a `doc.go`.
5. Replace the template view with a placeholder window titled with the app name.
6. First commit.

## Done when

- `go tool mygo dev` opens the window.
- `go test ./...` passes (the template's view test, adapted to the placeholder).
- `go vet ./...` and `go tool mygo vet` are clean.

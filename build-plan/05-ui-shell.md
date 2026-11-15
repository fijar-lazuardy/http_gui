# 05 - UI shell

**Result:** the window shows the workspace: a sidebar with the collections tree and the
environment picker, and an empty request pane. Creating, renaming and deleting items works
and persists.

## State (`internal/app`)

One struct holds everything that lasts; the view is a method on it and runs every frame.

```go
type App struct {
    win   *mygo.Window
    ws    *store.Workspace
    envID string            // chosen environment, "" for none
    open  *model.Request    // the request being edited
    tree  map[string]*bool  // folder ID -> open
    sidebarWidth float32
    // per-request editor state arrives in step 06
}

func (a *App) View(c *ui.Context)
```

MyGo rules that shape this (see `AGENTS.md`):

- Never store a `ui.Element` or the context's build values in `App`.
- Every row built in a loop gets `c.Key(item.ID)`.
- Actions that change the collection being looped over use `OnClick`, not `Clicked`.
- Goroutines change state only inside `win.Update(func() { ... })`.

## Layout

```
+--------------------+---------------------------------------------+
| [Environment  v]   |  (request pane, step 06)                    |
| [+ New] [Import]   |                                             |
|                    |                                             |
| v My API           |                                             |
|   v Auth           |                                             |
|       POST Login   |                                             |
|   GET  List users  |                                             |
+--------------------+---------------------------------------------+
```

- `ui.Split(c, &a.sidebarWidth, a.sidebar, a.main).Fill()`.
- Sidebar: `ui.Select` for the environment ("No environment" first), then `ui.Tree` with
  `ui.TreeItem` per folder and request. A request row shows its method as a short colored
  label before the name.
- Context menu on tree items (`ContextMenu`): New request, New folder, Rename, Duplicate,
  Delete. Delete asks with `ui.AlertDialog`.
- Rename uses `ui.EditableText` or a small `ui.Modal`, whichever is simpler in practice.
- Environments: a `ui.Modal` listing environments and globals, each edited with the
  key/value editor below.

## Key/value editor

Built once here, reused for variables now and for params, headers and form fields in step 06.

```go
func kvEditor(c *ui.Context, rows *[]model.KV, opt kvOptions) (changed bool)
```

Each row: enabled `ui.Checkbox`, key `ui.TextInput`, value `ui.TextInput` (`Password()` when
`Secret`), delete button. An empty row is always shown last; typing in it appends a row.
Rows are keyed by `KV.ID`.

## Saving

Edits save on change, debounced (`c.After`), one file at a time through `internal/store`.
No explicit save button in v1. Save errors show as a `c.Toast`.

## Tests (`ui.NewTester`)

- The tree shows a workspace built in `t.TempDir()`; clicking a request sets `a.open`.
- New request through the menu appears in the tree and exists on disk.
- Delete asks first, then removes the row and the file; cancelling keeps both.
- Key/value editor: typing in the empty row adds a row; unchecking sets `Enabled` false;
  delete removes the right row when several share a key.
- Choosing an environment in the select sets `a.envID`.

## Done when

`go test ./internal/app/...` passes and `go tool mygo dev` shows the shell against a real
workspace folder.

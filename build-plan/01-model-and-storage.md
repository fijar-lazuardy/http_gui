# 01 - Model and storage

**Result:** collections, requests and environments are Go values that round-trip to JSON
files in a workspace folder.

## Model (`internal/model`)

```go
type KV struct {            // params, headers, form fields, variables
    ID      string          // stable, for UI keys
    Key     string
    Value   string
    Enabled bool
    Secret  bool            // variables only: masked in the UI
    File    bool            // multipart only: Value is a file path
}

type BodyKind string        // "none" | "json" | "form-data" | "urlencoded"

type Body struct {
    Kind BodyKind
    Raw  string             // json
    Form []KV               // form-data and urlencoded
}

type Request struct {
    ID, Name   string
    Seq        int          // order within its folder
    Method     string
    URL        string       // as typed, may hold {{vars}} and a query string
    Params     []KV
    Headers    []KV
    Body       Body
    PreScript  Script
    PostScript Script
}

type Script struct {
    Lang   string           // "js" | "ts"
    Source string
}

type Folder struct {
    ID, Name string
    Seq      int
    Folders  []*Folder
    Requests []*Request
}

type Collection struct {
    Folder                  // the root
    Variables []KV
}

type Environment struct {
    ID, Name  string
    Variables []KV
}
```

No interfaces and no auth types yet: add them when a step needs them.

## Storage (`internal/store`)

```
<workspace>/
  globals.json                         []KV
  environments/<name>.json             Environment
  collections/<collection>/
    collection.json                    name, variables
    <request>.json                     Request
    <folder>/folder.json               name, seq
    <folder>/<request>.json
```

- The default workspace is `mygo.App.Path(mygo.PathUserData)/workspace`; the store takes
  the path as an argument so tests use `t.TempDir()` and `internal/store` never imports mygo.
- File names are slugs of the names (lower case, hyphens); a rename moves the file. IDs
  live inside the files. Two names with the same slug get `-2`, `-3`; a request named
  "Collection" or "Folder" does too, so it cannot overwrite `collection.json` or `folder.json`.
- Writes are atomic: write a temp file in the same directory, then rename.
- API: `Open(dir) (*Workspace, error)`, `SaveGlobals()`, `SaveEnvironment(e)`,
  `SaveCollection(c)`, `SaveFolder(parent, f)`, `SaveRequest(parent, r)`, `Delete(id)`.
  Save one thing at a time; no "save all". There is no `Rename`: saving an item whose name
  changed moves its file or directory. Saving under another parent moves it there.
- **The store reads and writes files only.** The caller owns the tree in memory: it appends
  a new request to its folder's `Requests` and then saves it, and removes it before or after
  `Delete`.
- A file that fails to parse is listed in `Workspace.Problems` with its path and skipped,
  never deleted. A folder whose `folder.json` is missing or broken still loads, under its
  directory's name.
- An item without an ID, or with one another file already has (a file copied by hand), gets
  a new ID as it loads.

## Tests

- Round trip: a workspace with nested folders, every body kind and both script languages
  saved, reopened, and equal. This guards the on-disk format, which users will keep in git.
- Rename and delete leave no stale files.
- Two requests whose names slug to the same file name do not overwrite each other.
- A corrupt file is reported and the rest of the workspace still loads.

## Done when

`go test ./internal/model/... ./internal/store/...` passes.

# 07 - Import Postman

**Result:** a Postman collection export and a Postman environment export become a collection
and an environment in the workspace.

## As built

Done, except for the last line of "Done when": no real export from Postman was tried, only
the hand-written fixtures. What differs from the plan below, or was left open in it:

- **Auth is inherited.** A request without its own `auth` gets the nearest one above it
  (folder, then collection), as in Postman; `noauth` stops that. `bearer` becomes an
  `Authorization` header, and so does `basic`, unless it holds `{{variables}}`, which would
  have to be encoded when the request is sent. A request that already has an enabled
  `Authorization` header keeps it. Every auth block is reported where the file has it.
- **Raw bodies that are not JSON are kept**, as the JSON kind, with a warning: the text is
  sent as written, as `application/json` unless a `Content-Type` header says otherwise.
- **Globals** have a field of their own, `Result.Globals`. The app adds them to the
  workspace's globals; a key that is already there keeps its value, and the report says so.
- **A query in the URL** is cut off and becomes `Params`: from `url.query[]` when the file
  has it, parsed (and percent-decoded) from the string otherwise.
- **Path variables** without a value stay in the URL as `:id`, with a warning.
- **Scripts:** besides the calls listed below, `pm.execution`, `pm.visualizer` and the old
  `postman.*` / `tests[]` API are warned of.
- **Left out without a warning:** descriptions, saved example responses (`response[]`),
  `protocolProfileBehavior`, and a URL given only as `host`/`path` parts without `raw`
  (reported as "has no URL").
- **The v1 collection format** is refused with a message to export as v2.1.
- **`Result` and `Warning`** are in `internal/importer`, shared with Bruno's importer
  (step 08); an environment is `Result.Environments[0]`.
- **UI:** the report dialog lists each file with what it became and its warnings. Files are
  read on the main thread after the file dialog closes; they are small.

## Input formats (`internal/importer/postman`)

- **Collection v2.1** (`info.schema` ends in `v2.1.0/collection.json`), and **v2.0**, which
  differs mainly in `url` and `auth` being plain strings or objects.
- **Environment** export (`values: [{key, value, enabled, type}]`), and the **globals**
  export of the same shape.

Detect the kind from the JSON itself, not the file name.

## Mapping

| Postman | Model |
| --- | --- |
| `info.name` | `Collection.Name` |
| `item[]` with `item` | `Folder`, recursively |
| `item[]` with `request` | `Request` |
| `request.method` | `Method` |
| `request.url.raw` (or the string) | `URL`, with the query string cut off |
| `request.url.query[]` | `Params` (`disabled` -> `Enabled: false`) |
| `request.header[]` | `Headers` |
| `body.mode = raw` + `options.raw.language = json` | `Body{Kind: json, Raw}` |
| `body.mode = urlencoded` | `Body{Kind: urlencoded, Form}` |
| `body.mode = formdata` | `Body{Kind: form-data, Form}`; `type: file` -> `File: true`, `src` as the path |
| `event[listen = prerequest].script.exec[]` | `PreScript{Lang: js}`, lines joined |
| `event[listen = test].script.exec[]` | `PostScript{Lang: js}` |
| `variable[]` | `Collection.Variables` |
| environment `values[]` | `Environment.Variables`; `type: secret` -> `Secret: true` |

`{{name}}` text needs no translation. Scripts need none either: step 04 implements `pm.*`.

## Not mapped, reported

The importer returns warnings beside the result, shown in the UI after import. Nothing is
dropped silently:

- `auth` blocks (v1 has no auth helpers). A `bearer` or `basic` block may be turned into an
  `Authorization` header; say so in the warning.
- Body modes `graphql`, `file`, and raw bodies in languages other than JSON (import raw text
  as JSON kind only if it parses; otherwise warn).
- Folder- and collection-level scripts (`event` on a folder or the collection).
- `pm.*` calls the subset in step 04 lacks (`pm.sendRequest`, `pm.cookies`, `pm.iterationData`):
  scan the script text and warn by request name.
- Path variables (`:id` in the URL with `url.variable[]`): substitute the given value into
  the URL and warn.

```go
func Import(data []byte) (*Result, error)
type Result struct {
    Collection  *model.Collection   // or nil
    Environment *model.Environment  // or nil
    Warnings    []Warning           // {Path: "Folder/Request", Message}
}
```

## UI

Sidebar "Import" opens `mygo.Dialog.Open` (JSON files, several allowed), imports each,
saves through `internal/store`, then shows a `ui.Modal` with what was imported and the
warnings. A name that already exists gets a numbered suffix; nothing is overwritten.

## Tests

Fixtures in `internal/importer/postman/testdata/`, hand-written and small, one concern each:

- v2.1 and v2.0 of the same collection import to equal models.
- Nested folders keep their order.
- Each body mode maps as in the table; disabled params and headers stay disabled.
- Scripts arrive with line breaks intact, and an imported pre-request script using
  `CryptoJS.HmacSHA256` and `pm.environment.set` runs in `internal/script`.
- An `auth` block, a GraphQL body and a folder-level script each produce a warning naming
  the item.
- An environment export with a secret imports with `Secret` set.
- Garbage JSON and a non-Postman JSON file return an error, not an empty collection.

## Done when

The tests pass, and a real exported collection imports, shows in the tree and sends.

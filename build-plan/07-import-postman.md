# 07 - Import Postman

**Result:** a Postman collection export and a Postman environment export become a collection
and an environment in the workspace.

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

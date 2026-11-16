# 08 - Import Bruno

**Result:** a Bruno collection becomes a collection (and its environments) in the workspace.

## As built

Done, except for the last line of "Done when": no collection made by Bruno itself was
tried, only fixtures written by hand to Bruno's grammar and schema.

**The formats, checked against Bruno's source (November 2026):**

1. **Folder of `.bru` files** with `bruno.json` at the root: imported (`bruno.ImportDir`).
   The grammar followed is `packages/bruno-lang/v2/src/bruToJson.js` and `envToJson.js`.
2. **YAML folder** (OpenCollection: `opencollection.yml` at the root, `.yml` requests), an
   option since Bruno 3.0: **not imported**. Go has no YAML decoder in its standard library,
   and a dependency needs the owner's yes. Such a folder is refused with a message saying so.
3. **Exported JSON** (`{name, version, items, environments, root}`, the schema in
   `packages/bruno-schema`): imported (`bruno.ImportJSON`).

What differs from the plan below, or was left open in it:

- **One mapping.** The `.bru` files are read into the values the exported JSON has, and one
  mapping takes those to the model, so the two forms cannot drift apart.
- **Shared with Postman's importer** (`internal/importer`): `Result`, `Warning` and
  `SplitQuery`. The same choices apply: auth is inherited (`inherit` takes the nearest
  folder's or the collection's, `none` stops it), bearer and basic become an
  `Authorization` header, and text bodies that are not JSON are kept with a warning.
- **Order** is Bruno's: by `seq`, then by name. `Seq` is kept as Bruno has it.
- **Folders without requests or a `folder.bru`** (scripts, certificates) are not imported.
  Hidden folders, `node_modules` and the root's `environments` are skipped.
- **Only `bruno.json` and `.bru` files are read.** A collection's folder may hold a `.env`
  file with secrets; nothing opens it. Links are not followed.
- **A file that does not parse** is skipped with a warning naming it and the line; the rest
  of the collection is imported.
- **Script calls:** any `bru.x` the script API lacks, and any `require` of a module other
  than `crypto`, `crypto-js` and `chai`, is warned of. A test holds the importer's list of
  `bru` functions to the script API's own.
- **Left out without a warning:** `settings`, saved `example` blocks, a pair's
  `@description` and other annotations, a form field's `@contentType`, and the docs of a
  folder or the collection.
- **Environments** come as workspace environments (Bruno's belong to a collection). A file's
  `extends` and `vars:externalsecrets` blocks are warned of.
- **UI:** "Import" is a menu: "Postman or Bruno files…" (JSON; the app tries Postman's
  format, then Bruno's) and "Bruno collection folder…".

## First: check the formats

Bruno has had more than one on-disk format. Before writing code, look at a collection made
by the current Bruno release and at its docs, and note here which of these exist:

1. **Classic folder collection:** `bruno.json` at the root, one `.bru` file per request,
   `folder.bru`, `collection.bru`, `environments/<name>.bru`.
2. **YAML folder collection** (the newer OpenCollection format), if current Bruno writes it.
3. **Exported JSON collection** (Bruno's "Export collection" to a single JSON file).

Plan for 1 and 3. Add 2 if step 1 of this file shows it is in use; it would be a YAML
decoder over the same mapping.

## `.bru` parser (`internal/importer/bruno`)

The `.bru` language is small: named blocks, of three kinds.

- **Dictionary blocks** `name { key: value }`: `meta`, `get`/`post`/`put`/..., `headers`,
  `params:query`, `params:path`, `body:form-urlencoded`, `body:multipart-form`, `vars`,
  `vars:pre-request`, `auth:*`. A key starting with `~` is disabled.
- **Text blocks** `name { ...free text... }`: `body:json`, `body:text`, `body:xml`,
  `script:pre-request`, `script:post-response`, `tests`, `docs`. The content is everything
  up to the closing brace at column 0, with the two-space indent removed.
- **List blocks** `name [ ... ]`: in environment files, `vars:secret [ a, b ]`.

Write a line-based parser for exactly this; no general grammar. Unknown blocks are kept by
name and reported as warnings.

## Mapping

| Bruno | Model |
| --- | --- |
| `bruno.json` `name` | `Collection.Name` |
| directories | `Folder`; `folder.bru` `meta.name` if present |
| `meta { name, seq }` | `Request.Name`, `Seq` |
| `get { url, body }` etc. | `Method`, `URL`; `body:` names the active body block |
| `params:query` | `Params` |
| `headers` | `Headers` |
| `body:json` | `Body{Kind: json}` |
| `body:form-urlencoded` | `Body{Kind: urlencoded}` |
| `body:multipart-form` | `Body{Kind: form-data}`; values `@file(path)` -> `File: true` |
| `script:pre-request` | `PreScript{Lang: js}` |
| `script:post-response` and `tests` | `PostScript{Lang: js}`, concatenated |
| `collection.bru` `vars:pre-request` | `Collection.Variables` |
| `environments/<name>.bru` `vars`, `vars:secret` | `Environment`; secrets come without values |

`{{name}}` is the same syntax. Scripts run through the `bru`/`req`/`res` shim of step 04.

## Not mapped, reported as warnings

- `auth:*` blocks (same treatment as Postman: bearer and basic may become a header).
- `vars:pre-request` / `vars:post-response` on a request, `assert` blocks, `docs`.
- `params:path`: substituted into the URL, with a warning.
- Body kinds other than the three supported.
- Folder- and collection-level scripts and headers.
- Script calls the shim lacks (`bru.runRequest`, `bru.sleep`, `require` of npm modules).
- Secret environment variables: imported as names with empty values; the user fills them in.

## UI

"Import" gains a choice: files (Postman JSON, Bruno JSON) or a folder (`mygo.Dialog.Open`
with `Directory`) for a Bruno collection. A folder is a Bruno collection if it holds
`bruno.json`. The result modal and no-overwrite rule from step 07 are reused.

## Tests

Fixtures in `testdata/`: a small collection folder written by hand to the format found above.

- Parser: each block kind, a disabled `~key`, a text block containing braces (a JSON body, a
  script with `if (x) { }`), and Windows line endings.
- A request per body kind maps as in the table.
- Folder nesting and `seq` order are kept.
- An environment file with a secret list imports the name with an empty value and a warning.
- An imported pre-request script using `bru.setVar` and `req.setHeader` runs in
  `internal/script` and changes the outgoing request.
- The exported-JSON form of the same collection imports to an equal model.
- A folder without `bruno.json` is refused with a clear error.

## Done when

The tests pass, and a real Bruno collection folder imports, shows in the tree and sends.

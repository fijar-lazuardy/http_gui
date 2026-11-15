# 03 - Variables

**Result:** `{{name}}` in a request is replaced by the variable's value before sending. With
`url = https://google.com`, a body of `{"target": "{{url}}"}` is sent as
`{"target": "https://google.com"}`.

## Scopes and precedence (`internal/vars`)

Highest wins, as in Postman:

1. **Runtime**: set by scripts with `pm.variables.set`, lives for one run.
2. **Environment**: the environment chosen in the picker; none may be chosen.
3. **Collection**: `Collection.Variables`.
4. **Global**: `globals.json`.

Disabled rows are ignored, and of rows with the same key the last wins (`vars.Map`).
Disabled params, headers and form fields are not resolved either, so a name used only in a
row that is not sent is not reported as missing.

```go
type Scopes struct {
    Runtime, Environment, Collection, Global map[string]string
}

func (s *Scopes) Lookup(name string) (value string, ok bool)
func (s *Scopes) Resolve(text string) (out string, missing []string)
func ResolveRequest(r *model.Request, s *Scopes) (resolved *model.Request, missing []string)
```

## Rules

- Syntax: `{{name}}`, spaces inside the braces allowed (`{{ name }}`).
- Applied to: URL, param keys and values, header keys and values, raw body, form keys and
  values (including file paths). Not applied to scripts: they call `pm.variables.get`.
- Substitution is textual. In a JSON body the user writes the quotes: `"{{token}}"` for a
  string, `{{count}}` for a number.
- A value may contain `{{other}}`; resolve up to a fixed depth (10) and stop, so a cycle
  cannot loop.
- **An unknown name stays as the literal `{{name}}`** and is returned in `missing`, so the
  UI can warn before or after sending. It is never replaced by an empty string.
- `ResolveRequest` returns a copy; the stored request keeps its `{{name}}` text.
- Dynamic variables, Postman's names, computed per occurrence: `{{$guid}}`,
  `{{$randomUUID}}`, `{{$timestamp}}`, `{{$isoTimestamp}}`, `{{$randomInt}}`. Nothing more
  until asked for.

## Runner (`internal/runner`)

Introduce the runner here, without scripts yet:

```go
type Result struct {
    Sent     *model.Request   // after resolution, what actually went out
    Response *httpx.Response
    Missing  []string
    Err      error
}

func Run(ctx context.Context, in Input) Result  // Input: request, scopes, httpx.Options
```

## Tests

- The example above, end to end through `runner.Run` against `httptest.Server`.
- Precedence: the same name in all four scopes resolves to runtime, then environment, and
  so on as scopes are removed.
- Each place a variable can appear (URL, param, header, each body kind) is resolved.
- Unknown names stay literal and are listed once in `missing`.
- `a = {{b}}`, `b = {{a}}` terminates.
- Dynamic variables: `{{$guid}}` twice in one body gives two values.
- The stored request is unchanged after a run.

## Done when

`go test ./internal/vars/... ./internal/runner/...` passes.

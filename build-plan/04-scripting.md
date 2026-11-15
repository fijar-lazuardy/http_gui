# 04 - Scripting

**Result:** a request's pre-request script runs before variables resolve, its post-response
script after the response arrives. Scripts are JavaScript or TypeScript and can hash, HMAC
and sign with keys.

## As built

Done. What differs from the plan below, or was left open in it:

- **`await` works without an event loop.** The script is the body of an async function
  (so top-level `await` and `return` work), and goja runs promise jobs itself. There are no
  timers and no requests, so a promise that is still pending when the script ends is an
  error. `goja_nodejs/eventloop` is not used.
- **`require`, not `import`.** Because the script is a function body, `import` statements
  are a syntax error. `require('crypto')`, `require('crypto-js')` and `require('chai')` work.
- **The sandbox.** `require` loads only those modules: its file loader always fails, and a
  test fails if a file on disk can be required. There is no `process`, `fetch` or
  `setTimeout`. Source maps are never read from files.
- **Errors name the user's line.** esbuild writes an inline source map and goja reads it,
  so `pre-request.ts:5` is line 5 as typed, also for TypeScript.
- **`pm.request.url`** has `toString()` (as typed, `{{names}}` and all) and `getHost()`,
  `getPath()`, `getQueryString()`, which answer for the URL **as it will be sent**:
  variables resolved and params in the query. That is what a signature covers. Postman's
  own return the unresolved parts.
- **`pm.response.to`**: `have.status(code or reason)`, `have.header(name[, value])`,
  `be.ok` (200), `be.success` (2xx). Nothing else of Postman's response assertions.
- **ECDSA** signs in DER by default, as Node does; `{key, dsaEncoding: 'ieee-p1363'}` gives
  r and s, as JWTs carry them.
- **Not supported:** keys encrypted with a passphrase, `createCipheriv` (use
  `CryptoJS.AES`), `pm.sendRequest`, `pm.cookies`, Postman's legacy `postman.*` and `tests[]`.
- **Embedded JS** is in `internal/script/js/`, not `vendor/`, which Go treats specially.
  crypto-js 4.2.0 and chai 4.5.0, minified, with their licenses and checksums.
- **Persisting variables** is `vars.Apply(rows, scope)`: the runner returns the scopes as
  the scripts left them in `Result.Scopes`, and the app writes each back to its rows and
  saves. The runner itself touches no files.
- **Canceling** a run during a script sets `Result.Err` to the context's error, not
  `ScriptErr`.

## Engine (`internal/script`)

- **goja** runs the script; one fresh `*goja.Runtime` per script run (a runtime is not safe
  for concurrent use and must not leak state between requests).
- **goja_nodejs** provides `require`, `console` and `Buffer`. `console.*` is captured into
  the run's console output instead of stdout.
- **TypeScript:** `esbuild`'s `api.Transform` with `Loader: api.LoaderTS`,
  `Format: api.FormatCommonJS` and a target goja supports. JS goes through the same call so
  newer syntax is lowered too. Transform errors are reported with line and column.
- **Timeout:** each run has a deadline (default 10 s); on expiry `vm.Interrupt` stops it.
- **Async:** if scripts are to `await`, run them on `goja_nodejs/eventloop` and wait for the
  loop to drain. Verify this first with a small test; if it is awkward, v1 scripts are
  synchronous and that is documented.
- No file system or network access from scripts in v1 (`pm.sendRequest` is later).

## Script API

Postman's `pm` object, the subset below. The same Go functions back a `bru`/`req`/`res`
shim so imported Bruno scripts run.

| Postman | Bruno shim | Does |
| --- | --- | --- |
| `pm.environment.get/set/unset/has` | `bru.getEnvVar` / `bru.setEnvVar` | environment scope |
| `pm.collectionVariables.get/set/unset/has` | `bru.getCollectionVar` | collection scope |
| `pm.globals.get/set/unset/has` | `bru.getGlobalEnvVar` / `bru.setGlobalEnvVar` | global scope |
| `pm.variables.get/set/has/replaceIn` | `bru.getVar` / `bru.setVar` / `bru.interpolate` | runtime scope; `get` reads through all scopes |
| `pm.request.method`, `.url`, `.headers.get/add/upsert/remove`, `.body.raw` | `req.getMethod/setMethod`, `getUrl/setUrl`, `getHeader/setHeader`, `getBody/setBody` | read and change the outgoing request (pre-request) |
| `pm.response.code`, `.status`, `.headers.get`, `.text()`, `.json()`, `.responseTime` | `res.status`, `res.headers`, `res.body`, `res.getResponseTime()` | read the response (post-response) |
| `pm.test(name, fn)` | `test(name, fn)` | a named check; a throw fails it |
| `pm.expect` | `expect` | chai's `expect`, embedded (see below) |

Changes to `set` variables persist to their scope's file after the run, except runtime.

## Crypto

Two surfaces, both needed:

1. **`require('crypto')`**, shaped like Node's so nothing new is invented, implemented in
   Go over the standard library:
   - `createHash(alg).update(data).digest(enc)`: md5, sha1, sha256, sha384, sha512.
   - `createHmac(alg, key).update(data).digest(enc)`.
   - `createSign(alg).update(data).sign(privateKeyPem, enc)` and `createVerify`:
     RSA PKCS#1 v1.5 (`RSA-SHA256` etc.), ECDSA.
   - `sign(null, data, key)` / `verify`: Ed25519; RSA-PSS through the key object's `padding`.
   - `publicEncrypt` / `privateDecrypt` (RSA-OAEP), `randomBytes`, `randomUUID`.
   - Keys as PEM strings: PKCS#1, PKCS#8, SEC1, and PKIX public keys.
   - Encodings: `hex`, `base64`, `base64url`, or a `Buffer`.
2. **`CryptoJS` global**: the `crypto-js` bundle embedded with `go:embed` and evaluated in
   the runtime. Existing Postman scripts call `CryptoJS.HmacSHA256(...)` and must keep working.

Also `atob`/`btoa`. Embedded JS (crypto-js, chai) lives in `internal/script/vendor/` with
its license file and version noted.

## Runner

Extend `runner.Run` to the full order: pre-script, resolve, send, post-script, persist.
`Result` gains `Console []LogLine`, `Tests []TestResult` and `ScriptErr`. A pre-script error
stops the run before sending; a post-script error keeps the response.

## Tests

- **Signature, end to end:** a pre-request script computes
  `HMAC-SHA256(secret, method + path + body)` and sets it as a header; the `httptest`
  server recomputes it in Go and compares. This is the feature's reason to exist.
- Hashes and HMACs match known vectors (RFC 4231 for HMAC-SHA256).
- RSA and Ed25519 signatures made in a script verify in Go with the public key, and the
  reverse.
- `CryptoJS.HmacSHA256` and `require('crypto').createHmac` agree.
- A TypeScript script with type annotations and an interface runs.
- `pm.environment.set` in a pre-script is visible to `{{name}}` in the same request, and to
  the next run after persisting.
- A post-script reads `pm.response.json()` and stores a field as a variable (the token flow).
- `pm.test` passing and failing are both reported; a syntax error reports its line.
- `while (true) {}` is interrupted at the deadline.
- Two runs do not share globals.

## Done when

`go test ./internal/script/... ./internal/runner/...` passes.

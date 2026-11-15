# 02 - HTTP engine

**Result:** `internal/httpx` turns a resolved `model.Request` into an `*http.Request`, sends
it, and returns everything the response pane shows. No UI, no variables, no scripts yet.

## API

```go
type Response struct {
    Status     string        // "200 OK"
    StatusCode int
    Proto      string
    Headers    http.Header
    Body       []byte
    Size       int64
    Duration   time.Duration // send to last body byte
    FinalURL   string        // after redirects
}

type Options struct {
    Timeout         time.Duration
    FollowRedirects bool
    InsecureTLS     bool
}

func Build(ctx context.Context, r *model.Request) (*http.Request, error)
func Send(ctx context.Context, r *model.Request, o Options) (*Response, error)
```

## Rules

- **Methods:** GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS; any other token the user types
  is passed through.
- **URL and params:** enabled `Params` are appended to the query string already in `URL`.
  A URL without a scheme gets `http://`.
- **Headers:** enabled rows only, in order; duplicates are kept (`Header.Add`). A `Host`
  header sets the request's host, since net/http ignores it among the headers.
- Multipart bodies, files included, are built in memory before sending. Stream them if
  large uploads matter.
- **Body:**
  - `json`: `Raw` sent as is, `Content-Type: application/json`. Invalid JSON is still sent;
    the UI warns (step 06), the engine does not block.
  - `urlencoded`: enabled rows as `application/x-www-form-urlencoded`.
  - `form-data`: `mime/multipart`; text rows as fields, rows with `File` as file parts read
    from the path in `Value`.
  - `none`: no body.
- A `Content-Type` header set by the user wins over the one the body kind implies, except
  for multipart, where the boundary must be the writer's.
- Cancelling `ctx` aborts the request; the UI's Cancel button uses this.
- Errors are returned as they are (DNS, TLS, timeout, refused); no retries.
- One `http.Client` per `Options` value, without a cookie jar for now.

## Tests (against `httptest.Server`)

- Each method arrives as sent.
- Params merge with a query already in the URL, and disabled rows are left out.
- Each body kind arrives with the right `Content-Type` and decodes on the server to what
  was entered, including a multipart file part.
- A user `Content-Type` overrides the JSON default.
- Redirects are followed or not, as `Options` says.
- A cancelled context returns promptly with `context.Canceled`.
- `Duration` and `Size` are set.

## Done when

`go test ./internal/httpx/...` passes.

package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"http_gui/internal/model"
)

func on(key, value string) model.KV  { return model.KV{Key: key, Value: value, Enabled: true} }
func off(key, value string) model.KV { return model.KV{Key: key, Value: value} }

// seen is what a test server received.
type seen struct {
	method, query, host string
	header              http.Header
	body                string
	form                map[string][]string // of a form body, parsed by the server
	file                string              // content of the uploaded file "file"
	fileName            string
}

// capture starts a server that records the request it gets.
func capture(t *testing.T) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.method, s.query, s.host, s.header = r.Method, r.URL.RawQuery, r.Host, r.Header
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "multipart/form-data"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("the server could not parse the multipart body: %v", err)
				return
			}
			s.form = r.MultipartForm.Value
			if f, h, err := r.FormFile("file"); err == nil {
				data, _ := io.ReadAll(f)
				s.file, s.fileName = string(data), h.Filename
			}
		case ct == "application/x-www-form-urlencoded":
			r.ParseForm()
			s.form = r.PostForm
		default:
			data, _ := io.ReadAll(r.Body)
			s.body = string(data)
		}
		w.Header().Set("X-Reply", "yes")
		io.WriteString(w, "hello")
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func send(t *testing.T, r *model.Request, o Options) *Response {
	t.Helper()
	resp, err := Send(context.Background(), r, o)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The method chosen is the one sent, including one the app has no entry
// for.
func TestMethods(t *testing.T) {
	srv, s := capture(t)
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "PURGE"} {
		send(t, &model.Request{Method: m, URL: srv.URL}, Options{})
		if s.method != m {
			t.Errorf("sent %s, the server got %s", m, s.method)
		}
	}
	send(t, &model.Request{Method: " post ", URL: srv.URL}, Options{})
	if s.method != "POST" {
		t.Errorf("a method typed as %q arrived as %s", " post ", s.method)
	}
}

// The parameter rows add to what the URL already has, and a row switched
// off is not sent.
func TestParamsMergeWithTheURLQuery(t *testing.T) {
	srv, s := capture(t)
	send(t, &model.Request{
		URL:    srv.URL + "/search?page=1",
		Params: []model.KV{on("q", "go lang&more"), off("debug", "1"), on("tag", "a"), on("tag", "b")},
	}, Options{})
	if want := "page=1&q=go+lang%26more&tag=a&tag=b"; s.query != want {
		t.Errorf("query %q, want %q", s.query, want)
	}
}

func TestHeaders(t *testing.T) {
	srv, s := capture(t)
	send(t, &model.Request{
		URL: srv.URL,
		Headers: []model.KV{
			on("X-One", "1"), on("X-Many", "a"), on("X-Many", "b"),
			off("X-Off", "no"), on("Host", "api.example.com"),
		},
	}, Options{})
	if s.header.Get("X-One") != "1" || len(s.header.Values("X-Many")) != 2 {
		t.Errorf("headers %v", s.header)
	}
	if s.header.Get("X-Off") != "" {
		t.Error("a header switched off was sent")
	}
	// A Host header is how a request reaches a virtual host by IP.
	if s.host != "api.example.com" {
		t.Errorf("host %q, want the Host header's", s.host)
	}
}

// A JSON body goes out byte for byte: the app must not reformat what a
// signature may have been computed over.
func TestJSONBody(t *testing.T) {
	srv, s := capture(t)
	raw := "{\n  \"name\": \"Ada\",  \"n\": 1\n}"
	r := &model.Request{Method: "POST", URL: srv.URL, Body: model.Body{Kind: model.BodyJSON, Raw: raw}}
	send(t, r, Options{})
	if s.body != raw {
		t.Errorf("body %q, want %q", s.body, raw)
	}
	if ct := s.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}

	// APIs with media types of their own need theirs kept.
	r.Headers = []model.KV{on("content-type", "application/vnd.api+json")}
	send(t, r, Options{})
	if ct := s.header.Values("Content-Type"); len(ct) != 1 || ct[0] != "application/vnd.api+json" {
		t.Errorf("Content-Type %q, want the user's alone", ct)
	}
}

func TestURLEncodedBody(t *testing.T) {
	srv, s := capture(t)
	send(t, &model.Request{Method: "POST", URL: srv.URL, Body: model.Body{
		Kind: model.BodyURLEncoded,
		Form: []model.KV{on("name", "Ada Lovelace"), on("sym", "a&b=c"), off("skip", "1")},
	}}, Options{})
	if got := s.form["name"]; len(got) != 1 || got[0] != "Ada Lovelace" {
		t.Errorf("name %q", got)
	}
	if got := s.form["sym"]; len(got) != 1 || got[0] != "a&b=c" {
		t.Errorf("sym %q: the value was not escaped", got)
	}
	if _, ok := s.form["skip"]; ok {
		t.Error("a field switched off was sent")
	}
}

func TestMultipartBody(t *testing.T) {
	srv, s := capture(t)
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("file content"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &model.Request{
		Method: "POST", URL: srv.URL,
		// Without the writer's boundary the server cannot split the
		// parts, so this header must not win.
		Headers: []model.KV{on("Content-Type", "multipart/form-data")},
		Body: model.Body{Kind: model.BodyFormData, Form: []model.KV{
			on("title", "Q3"), off("skip", "1"),
			{Key: "file", Value: path, Enabled: true, File: true},
		}},
	}
	send(t, r, Options{})
	if got := s.form["title"]; len(got) != 1 || got[0] != "Q3" {
		t.Errorf("title %q", got)
	}
	if _, ok := s.form["skip"]; ok {
		t.Error("a field switched off was sent")
	}
	if s.file != "file content" || s.fileName != "report.txt" {
		t.Errorf("file %q named %q", s.file, s.fileName)
	}

	r.Body.Form[2].Value = filepath.Join(t.TempDir(), "missing.txt")
	if _, err := Send(context.Background(), r, Options{}); err == nil || !strings.Contains(err.Error(), `"file"`) {
		t.Errorf("a missing file must fail naming its field, got %v", err)
	}
}

func TestNoBody(t *testing.T) {
	srv, s := capture(t)
	send(t, &model.Request{Method: "POST", URL: srv.URL, Body: model.Body{Kind: model.BodyNone, Raw: "left over"}}, Options{})
	if s.body != "" || s.header.Get("Content-Type") != "" {
		t.Errorf("body %q, Content-Type %q, want none", s.body, s.header.Get("Content-Type"))
	}
}

func TestResponse(t *testing.T) {
	srv, _ := capture(t)
	resp := send(t, &model.Request{URL: srv.URL}, Options{})
	if resp.StatusCode != 200 || resp.Status != "200 OK" || string(resp.Body) != "hello" {
		t.Errorf("response %+v", resp)
	}
	if resp.Size != 5 || resp.Duration <= 0 || resp.Headers.Get("X-Reply") != "yes" || resp.Proto == "" {
		t.Errorf("size %d, duration %v, headers %v", resp.Size, resp.Duration, resp.Headers)
	}
}

// People paste hosts without a scheme, as localhost:8080/path, which
// net/url would read as a scheme of its own.
func TestURLWithoutScheme(t *testing.T) {
	srv, s := capture(t)
	send(t, &model.Request{URL: strings.TrimPrefix(srv.URL, "http://") + "/x?a=1"}, Options{})
	if s.query != "a=1" {
		t.Errorf("the request did not arrive, query %q", s.query)
	}
	if _, err := Send(context.Background(), &model.Request{URL: "  "}, Options{}); err == nil {
		t.Error("an empty URL must be an error")
	}
}

// Seeing a 302 itself, with its Location, is what debugging a login flow
// needs; following it is what most requests want.
func TestRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "b") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp := send(t, &model.Request{URL: srv.URL + "/a"}, Options{FollowRedirects: true})
	if resp.StatusCode != 200 || resp.FinalURL != srv.URL+"/b" {
		t.Errorf("following: status %d at %s", resp.StatusCode, resp.FinalURL)
	}
	resp = send(t, &model.Request{URL: srv.URL + "/a"}, Options{})
	if resp.StatusCode != 302 || resp.Headers.Get("Location") != "/b" {
		t.Errorf("not following: status %d, Location %q", resp.StatusCode, resp.Headers.Get("Location"))
	}
}

// hang starts a server whose handler waits for the client to give up.
func hang(t *testing.T) (srv *httptest.Server, started chan struct{}) {
	started = make(chan struct{}, 1)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv, started
}

// The Cancel button relies on this.
func TestCancel(t *testing.T) {
	srv, started := hang(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := Send(ctx, &model.Request{URL: srv.URL}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled", err)
	}
}

func TestTimeout(t *testing.T) {
	srv, _ := hang(t)
	_, err := Send(context.Background(), &model.Request{URL: srv.URL}, Options{Timeout: 50 * time.Millisecond})
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("error %v, want a timeout", err)
	}
}

// Certificates are verified unless the user turns that off, as for a
// local server with a self-signed one.
func TestInsecureTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := Send(context.Background(), &model.Request{URL: srv.URL}, Options{}); err == nil {
		t.Error("a self-signed certificate was accepted")
	}
	if _, err := Send(context.Background(), &model.Request{URL: srv.URL}, Options{InsecureTLS: true}); err != nil {
		t.Errorf("with InsecureTLS: %v", err)
	}
}

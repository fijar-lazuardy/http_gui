package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"http_gui/internal/model"
)

// received is a request as the test server read it.
type received struct {
	method, path, contentType string
	header                    http.Header
	body                      string
	form                      map[string]string
}

// echoServer answers every request with 200 and "pong", and passes what it
// read to the test.
func echoServer(t *testing.T) (*httptest.Server, chan received) {
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := received{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"), header: r.Header, form: map[string]string{}}
		// net/http parses no form in the body of a GET: read it here.
		if strings.HasPrefix(rec.contentType, "multipart/") {
			r.ParseMultipartForm(1 << 20)
			for k := range r.MultipartForm.Value {
				rec.form[k] = r.MultipartForm.Value[k][0]
			}
		} else {
			b, _ := io.ReadAll(r.Body)
			rec.body = string(b)
		}
		if rec.contentType == "application/x-www-form-urlencoded" {
			q, _ := url.ParseQuery(rec.body)
			for k := range q {
				rec.form[k] = q.Get(k)
			}
		}
		got <- rec
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "pong")
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// openNew opens a new request with no URL, in My API.
func (f *fixture) openNew() *model.Request {
	f.t.Helper()
	f.menu("My API", "New request")
	return f.a.open
}

// typeURL types a URL into the empty URL field.
func (f *fixture) typeURL(u string) {
	f.t.Helper()
	f.click("URL")
	f.tt.Type(u)
}

// send clicks Send and waits for the run to report back.
func (f *fixture) send() {
	f.t.Helper()
	f.click("Send")
	(<-f.updates)()
	f.tt.Frame()
}

// The heart of the app: what is typed in the window is sent, and the
// response shows.
func TestSendShowsResponse(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	f.openNew()
	f.typeURL(srv.URL + "/ping")
	f.send()
	if r := <-got; r.method != "GET" || r.path != "/ping" {
		t.Errorf("the server got %s %s", r.method, r.path)
	}
	if !f.tt.HasText("200 OK") {
		t.Errorf("no status shown: %q", f.tt.Texts())
	}
	if body := f.a.response.Buffer.String(); body != "pong" {
		t.Errorf("the response pane shows %q", body)
	}
}

// Switching away from a request and back keeps its response.
func TestResponsePersistsAcrossSwitch(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	reqA := f.openNew()
	f.typeURL(srv.URL + "/a")
	f.send()
	<-got
	if !f.tt.HasText("200 OK") {
		t.Fatalf("no response for request A: %q", f.tt.Texts())
	}

	// Switch to another request and back.
	f.click("List users")
	if f.tt.HasText("200 OK") {
		t.Error("request B shows request A's response")
	}
	f.click(reqA.Name)
	if !f.tt.HasText("200 OK") {
		t.Errorf("response lost after switching back: %q", f.tt.Texts())
	}
	if body := f.a.response.Buffer.String(); body != "pong" {
		t.Errorf("response body after switch: %q, want %q", body, "pong")
	}
}

func TestMethodSelect(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	f.openNew()
	f.click("Method")
	f.click("DELETE")
	f.typeURL(srv.URL)
	f.send()
	if r := <-got; r.method != "DELETE" {
		t.Errorf("chose DELETE, the server got %s", r.method)
	}
}

func TestHeaderReachesServer(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	f.openNew()
	f.typeURL(srv.URL)
	f.click("Headers")
	f.click("New key")
	f.tt.Type("X-Api-Key")
	f.click("Value")
	f.tt.Type("secret")
	f.send()
	if r := <-got; r.header.Get("X-Api-Key") != "secret" {
		t.Errorf("the header added did not arrive: %v", r.header)
	}
}

// Each kind of body arrives as the user entered it, decoded by the server
// as that kind.
func TestBodyKinds(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv, got := echoServer(t)
		f := newFixture(t)
		f.openNew()
		f.typeURL(srv.URL)
		f.click("Body")
		f.click("JSON")
		f.click("JSON body")
		f.tt.Type(`{"user": "ada"}`)
		f.send()
		r := <-got
		if r.contentType != "application/json" || r.body != `{"user": "ada"}` {
			t.Errorf("the server got %q: %q", r.contentType, r.body)
		}
	})
	for _, kind := range []string{"Form", "URL-encoded"} {
		t.Run(kind, func(t *testing.T) {
			srv, got := echoServer(t)
			f := newFixture(t)
			f.openNew()
			f.typeURL(srv.URL)
			f.click("Body")
			f.click(kind)
			f.click("New key")
			f.tt.Type("user")
			f.click("Value")
			f.tt.Type("ada lovelace")
			f.send()
			if r := <-got; r.form["user"] != "ada lovelace" {
				t.Errorf("the server decoded %q from %q", r.form, r.contentType)
			}
		})
	}
}

// Parameters and the URL's query are one thing shown twice: editing one
// changes the other.
func TestParamsFollowURL(t *testing.T) {
	f := newFixture(t)
	r := f.openNew()
	f.typeURL("example.com/search?q=go")
	if len(r.Params) != 1 || r.Params[0].Key != "q" || r.Params[0].Value != "go" || r.URL != "example.com/search" {
		t.Fatalf("the query typed made URL %q and params %+v", r.URL, r.Params)
	}
	f.click("New key")
	f.tt.Type("page")
	if f.a.urlText != "example.com/search?q=go&page=" {
		t.Errorf("adding a parameter made the URL %q", f.a.urlText)
	}
}

// A variable resolves from the environment chosen; without one, the user
// sees which name has no value before sending.
func TestVariableFromEnvironment(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	env := f.a.ws.Environments[0]
	env.Variables = []model.KV{{ID: "1", Key: "base", Value: srv.URL, Enabled: true}}
	f.openNew()
	f.typeURL("{{base}}/users")
	if !f.tt.HasText("No value for {{base}}") {
		t.Errorf("the missing variable is not shown: %q", f.tt.Texts())
	}
	f.click(noEnvironment)
	f.click("Staging")
	if f.tt.HasText("No value for {{base}}") {
		t.Error("the variable is still shown as missing with Staging chosen")
	}
	f.send()
	if r := <-got; r.path != "/users" {
		t.Errorf("the server got %s", r.path)
	}
}

// Cancel stops a request that hangs, and nothing of it shows afterwards.
func TestCancel(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	f := newFixture(t)
	f.openNew()
	f.typeURL(srv.URL)
	f.click("Send")
	<-entered
	if !f.tt.HasText("Sending…") {
		t.Errorf("no sign of sending: %q", f.tt.Texts())
	}
	f.click("Cancel")
	(<-f.updates)() // the run's late result
	f.tt.Frame()
	if f.a.sending || f.a.result != nil || !f.tt.HasText("Send") || f.tt.HasText("Sending…") {
		t.Errorf("after Cancel: sending %v, result %+v, texts %q", f.a.sending, f.a.result, f.tt.Texts())
	}
}

// Ctrl+Enter sends the request from anywhere, not just the URL field.
func TestCtrlEnterSends(t *testing.T) {
	srv, got := echoServer(t)
	f := newFixture(t)
	f.openNew()
	f.typeURL(srv.URL + "/ctrl")
	f.tt.Key(ui.Ctrl, ui.KeyEnter)
	(<-f.updates)()
	f.tt.Frame()
	if r := <-got; r.method != "GET" || r.path != "/ctrl" {
		t.Errorf("Ctrl+Enter sent %s %s, want GET /ctrl", r.method, r.path)
	}
	if !f.tt.HasText("200 OK") {
		t.Errorf("no response shown after Ctrl+Enter: %q", f.tt.Texts())
	}
}

// A failing test in a post-response script is what the user looks for in
// the Tests tab.
func TestFailingTestShows(t *testing.T) {
	srv, _ := echoServer(t)
	f := newFixture(t)
	r := f.openNew()
	r.PostScript = model.Script{Lang: "js", Source: `pm.test("created", () => pm.response.to.have.status(201))`}
	f.typeURL(srv.URL)
	f.send()
	f.click("Tests 0/1")
	found := false
	for _, s := range f.tt.Texts() {
		found = found || strings.HasPrefix(s, "✗ created")
	}
	if !found {
		t.Errorf("the failed test is not shown: %q", f.tt.Texts())
	}
}

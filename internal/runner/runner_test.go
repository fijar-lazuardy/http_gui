package runner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"http_gui/internal/model"
	"http_gui/internal/script"
	"http_gui/internal/vars"
)

// The feature as it was asked for: with a variable url of
// https://google.com, {{url}} in a body reaches the server as the URL, not
// as the text {{url}}.
func TestVariablesReachTheServer(t *testing.T) {
	var path, header, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		path, header, body = r.URL.Path, r.Header.Get("Authorization"), string(data)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	req := &model.Request{
		Method:  "POST",
		URL:     "{{base}}/redirects",
		Headers: []model.KV{{Key: "Authorization", Value: "Bearer {{token}}", Enabled: true}},
		Body:    model.Body{Kind: model.BodyJSON, Raw: `{"target": "{{url}}", "note": "{{typo}}"}`},
	}
	res := Run(context.Background(), Input{
		Request: req,
		Scopes: &vars.Scopes{
			Environment: vars.Map([]model.KV{
				{Key: "url", Value: "https://google.com", Enabled: true},
				{Key: "base", Value: srv.URL, Enabled: true},
			}),
			Global: map[string]string{"token": "abc"},
		},
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if path != "/redirects" || header != "Bearer abc" {
		t.Errorf("the server got path %q and Authorization %q", path, header)
	}
	if want := `{"target": "https://google.com", "note": "{{typo}}"}`; body != want {
		t.Errorf("the server got the body %s\nwant %s", body, want)
	}
	if !reflect.DeepEqual(res.Missing, []string{"typo"}) {
		t.Errorf("missing %v, want the mistyped name", res.Missing)
	}
	if string(res.Response.Body) != "ok" || res.Sent.URL != srv.URL+"/redirects" {
		t.Errorf("result %+v", res)
	}
	if req.URL != "{{base}}/redirects" {
		t.Error("the run changed the request as the user wrote it")
	}
}

// A run that cannot be sent says why, and has no response to show.
func TestErrorIsReturned(t *testing.T) {
	res := Run(context.Background(), Input{Request: &model.Request{URL: ""}})
	if res.Err == nil || res.Response != nil {
		t.Errorf("result %+v, want an error and no response", res)
	}
}

// signingServer answers 200 to requests whose X-Signature is the
// HMAC-SHA256, under secret, of the method, the path with its query, and
// the body, and 401 to the others. It counts the requests it gets.
func signingServer(t *testing.T, secret string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(r.Method + r.URL.RequestURI()))
		mac.Write(body)
		if r.Header.Get("X-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			http.Error(w, `{"error": "bad signature"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"token": "t-123"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const signScript = `
	const crypto = require('crypto');
	const url = pm.request.url;
	const body = pm.variables.replaceIn(pm.request.body.raw);
	const signature = crypto.createHmac('sha256', pm.environment.get('secret'))
		.update(pm.request.method + url.getPath() + '?' + url.getQueryString() + body)
		.digest('hex');
	pm.request.headers.upsert({key: 'X-Signature', value: signature});
`

func signedRequest(pre string) *model.Request {
	return &model.Request{
		Method:    "POST",
		URL:       "{{base}}/orders?page=1",
		Params:    []model.KV{{Key: "sort", Value: "{{order}}", Enabled: true}},
		Body:      model.Body{Kind: model.BodyJSON, Raw: `{"user": "{{user}}", "n": 1}`},
		PreScript: model.Script{Lang: "js", Source: pre},
	}
}

// What scripts are for: a server that checks a signature over the request
// accepts the one a pre-request script computes, over the request as it
// is sent, with its variables resolved.
func TestPreRequestScriptSignsTheRequest(t *testing.T) {
	srv, _ := signingServer(t, "s3cret")
	env := func() map[string]string {
		return map[string]string{"base": srv.URL, "secret": "s3cret", "user": "ada", "order": "asc"}
	}

	res := Run(context.Background(), Input{Request: signedRequest(signScript), Scopes: &vars.Scopes{Environment: env()}})
	if res.Err != nil || res.ScriptErr != nil {
		t.Fatalf("errors %v, %v", res.Err, res.ScriptErr)
	}
	if res.Response.StatusCode != 200 {
		t.Errorf("the server refused the signature: %s %s", res.Response.Status, res.Response.Body)
	}

	// The same script in TypeScript.
	ts := signedRequest(strings.Replace(signScript, "const url =", "const url: { getPath(): string; getQueryString(): string } =", 1))
	ts.PreScript.Lang = "ts"
	if res := Run(context.Background(), Input{Request: ts, Scopes: &vars.Scopes{Environment: env()}}); res.ScriptErr != nil || res.Response.StatusCode != 200 {
		t.Errorf("TypeScript: %v, %+v", res.ScriptErr, res.Response)
	}

	// Without the script the server refuses, so the 200 above is the
	// script's doing.
	if res := Run(context.Background(), Input{Request: signedRequest(""), Scopes: &vars.Scopes{Environment: env()}}); res.Response.StatusCode != 401 {
		t.Errorf("unsigned request: %s", res.Response.Status)
	}
}

// A variable a pre-request script sets is in the request it runs before,
// and, once written back to its rows, in the runs after it.
func TestScriptVariablesReachTheRequestAndPersist(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Nonce") + " " + r.Header.Get("X-Run")
	}))
	defer srv.Close()

	rows := []model.KV{{ID: "1", Key: "base", Value: srv.URL, Enabled: true}}
	req := &model.Request{
		URL:     "{{base}}",
		Headers: []model.KV{{Key: "X-Nonce", Value: "{{nonce}}", Enabled: true}, {Key: "X-Run", Value: "{{run}}", Enabled: true}},
		PreScript: model.Script{Source: `
			if (!pm.environment.has('nonce')) pm.environment.set('nonce', 'n-1');
			pm.variables.set('run', 'only this one');
		`},
	}
	res := Run(context.Background(), Input{Request: req, Scopes: &vars.Scopes{Environment: vars.Map(rows)}})
	if res.Err != nil || res.ScriptErr != nil || got != "n-1 only this one" || len(res.Missing) != 0 {
		t.Fatalf("first run: got %q, missing %v, errors %v %v", got, res.Missing, res.Err, res.ScriptErr)
	}

	rows, changed := vars.Apply(rows, res.Scopes.Environment)
	if !changed || len(rows) != 2 {
		t.Fatalf("rows after the run: %+v", rows)
	}
	// The next run has the environment's variable without setting it, and
	// not the one that was the first run's own.
	req.PreScript.Source = ""
	res = Run(context.Background(), Input{Request: req, Scopes: &vars.Scopes{Environment: vars.Map(rows)}})
	if got != "n-1 {{run}}" || !reflect.DeepEqual(res.Missing, []string{"run"}) {
		t.Errorf("second run: got %q, missing %v", got, res.Missing)
	}
}

// The login flow: the post-response script keeps the token the response
// has, and its tests and console lines come back with those of the
// pre-request script.
func TestPostResponseScript(t *testing.T) {
	srv, _ := signingServer(t, "s3cret")
	req := signedRequest(signScript + "\nconsole.log('signed');")
	req.PostScript = model.Script{Source: `
		pm.test('ok', () => pm.response.to.have.status(200));
		pm.test('has a token', () => pm.expect(pm.response.json().token).to.be.a('number'));
		pm.environment.set('token', pm.response.json().token);
		console.log('sent to', pm.request.url.toString());
	`}
	res := Run(context.Background(), Input{Request: req, Scopes: &vars.Scopes{
		Environment: map[string]string{"base": srv.URL, "secret": "s3cret", "user": "ada", "order": "asc"},
	}})
	if res.Err != nil || res.ScriptErr != nil {
		t.Fatalf("errors %v, %v", res.Err, res.ScriptErr)
	}
	if res.Scopes.Environment["token"] != "t-123" {
		t.Errorf("token %q", res.Scopes.Environment["token"])
	}
	if len(res.Tests) != 2 || !res.Tests[0].Passed || res.Tests[1].Passed {
		t.Errorf("tests %+v", res.Tests)
	}
	// The post-response script sees the request as it was sent.
	want := []script.LogLine{{Level: "log", Text: "signed"}, {Level: "log", Text: "sent to " + srv.URL + "/orders?page=1"}}
	if !reflect.DeepEqual(res.Console, want) {
		t.Errorf("console %+v, want %+v", res.Console, want)
	}
}

// A request whose signature could not be computed must not go out
// unsigned.
func TestPreRequestErrorStopsTheRun(t *testing.T) {
	srv, hits := signingServer(t, "s3cret")
	req := signedRequest("console.log('start');\nthrow new Error('no key');")
	res := Run(context.Background(), Input{Request: req, Scopes: &vars.Scopes{Environment: map[string]string{"base": srv.URL}}})
	if res.ScriptErr == nil || !strings.Contains(res.ScriptErr.Error(), "no key") {
		t.Errorf("script error %v", res.ScriptErr)
	}
	if *hits != 0 || res.Response != nil || res.Sent != nil || res.Err != nil {
		t.Errorf("the request was sent: hits %d, result %+v", *hits, res)
	}
	if len(res.Console) != 1 {
		t.Errorf("console %+v, want what the script logged before it failed", res.Console)
	}
}

// A script that fails on the response must not hide the response.
func TestPostResponseErrorKeepsTheResponse(t *testing.T) {
	srv, _ := signingServer(t, "s3cret")
	req := signedRequest("")
	req.PostScript = model.Script{Source: "pm.response.json().missing.field"}
	res := Run(context.Background(), Input{Request: req, Scopes: &vars.Scopes{Environment: map[string]string{"base": srv.URL}}})
	if res.ScriptErr == nil || res.Err != nil {
		t.Errorf("errors %v, %v, want the script's alone", res.Err, res.ScriptErr)
	}
	if res.Response == nil || res.Response.StatusCode != 401 {
		t.Errorf("response %+v", res.Response)
	}
}

// Cancel stops a run whose script is the slow part.
func TestCancelDuringAScript(t *testing.T) {
	srv, hits := signingServer(t, "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	res := Run(ctx, Input{Request: signedRequest("while (true) {}"), Scopes: &vars.Scopes{Environment: map[string]string{"base": srv.URL}}})
	if !errors.Is(res.Err, context.Canceled) || res.ScriptErr != nil || *hits != 0 {
		t.Errorf("result %+v, hits %d", res, *hits)
	}
}

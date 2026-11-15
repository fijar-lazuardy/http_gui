package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"http_gui/internal/model"
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

package postman

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"http_gui/internal/importer"
	"http_gui/internal/model"
	"http_gui/internal/script"
	"http_gui/internal/vars"
)

func load(t *testing.T, name string) *importer.Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// at returns the request at a path such as "Auth/Login".
func at(t *testing.T, col *model.Collection, path string) *model.Request {
	t.Helper()
	f := &col.Folder
	names := strings.Split(path, "/")
	for _, name := range names[:len(names)-1] {
		i := slices.IndexFunc(f.Folders, func(sub *model.Folder) bool { return sub.Name == name })
		if i < 0 {
			t.Fatalf("%s: no folder %q", path, name)
		}
		f = f.Folders[i]
	}
	i := slices.IndexFunc(f.Requests, func(r *model.Request) bool { return r.Name == names[len(names)-1] })
	if i < 0 {
		t.Fatalf("no request %s", path)
	}
	return f.Requests[i]
}

// show writes rows without their IDs, which are random.
func show(rows []model.KV) string {
	var parts []string
	for _, kv := range rows {
		s := kv.Key + "=" + kv.Value
		if !kv.Enabled {
			s += " (off)"
		}
		if kv.File {
			s += " (file)"
		}
		if kv.Secret {
			s += " (secret)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// outline writes everything in a folder, a line an item, in the order the
// items have.
func outline(f *model.Folder, indent string) []string {
	var out []string
	for _, sub := range f.Folders {
		out = append(out, fmt.Sprintf("%s%d %s/", indent, sub.Seq, sub.Name))
		out = append(out, outline(sub, indent+"  ")...)
	}
	for _, r := range f.Requests {
		out = append(out, fmt.Sprintf("%s%d %s: %s %s params[%s] headers[%s] body %s %q form[%s] pre %q post %q",
			indent, r.Seq, r.Name, r.Method, r.URL, show(r.Params), show(r.Headers),
			r.Body.Kind, r.Body.Raw, show(r.Body.Form), r.PreScript, r.PostScript))
	}
	return out
}

// Postman exports two formats, and which one a user has depends on a
// choice in its export dialog: both must give the same collection.
func TestVersionsAgree(t *testing.T) {
	new, old := load(t, "collection-v2.1.json"), load(t, "collection-v2.0.json")
	a, b := outline(&new.Collection.Folder, ""), outline(&old.Collection.Folder, "")
	if len(a) == 0 {
		t.Fatal("nothing imported")
	}
	if !slices.Equal(a, b) {
		t.Errorf("v2.1 and v2.0 differ:\nv2.1:\n%s\nv2.0:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
	if x, y := show(new.Collection.Variables), show(old.Collection.Variables); x != y {
		t.Errorf("variables differ: %q and %q", x, y)
	}
}

// An imported request must send what Postman would have sent: the same
// URL, parameters, headers and body, with what was off still off.
func TestCollection(t *testing.T) {
	res := load(t, "collection-v2.1.json")
	col := res.Collection
	if col.Name != "Shop API" {
		t.Errorf("name %q", col.Name)
	}
	if got := show(col.Variables); got != "baseUrl=https://shop.example; old=x (off)" {
		t.Errorf("variables: %s", got)
	}

	const bearer = "; Authorization=Bearer {{token}}"
	for _, tc := range []struct {
		path, method, url, params, headers string
		kind                               model.BodyKind
		raw, form                          string
	}{
		// The collection's bearer token goes to every request but Login,
		// which says it has no auth.
		{path: "Auth/Login", method: "POST", url: "{{baseUrl}}/login", headers: "X-Signature={{signature}}",
			kind: model.BodyJSON, raw: `{"user":"ann"}`},
		{path: "Auth/Tokens/Refresh", method: "POST", url: "{{baseUrl}}/refresh", headers: bearer[2:], kind: model.BodyNone},
		// The query leaves the URL: it would be sent twice otherwise.
		{path: "List items", method: "GET", url: "{{baseUrl}}/items", params: "page=2; q=a b",
			headers: "Accept=application/json; X-Debug=1 (off)" + bearer, kind: model.BodyNone},
		{path: "Search", method: "GET", url: "{{baseUrl}}/search", params: "q=tea; draft=1 (off)",
			headers: bearer[2:], kind: model.BodyNone},
		{path: "Admin/Upload", method: "POST", url: "{{baseUrl}}/upload", headers: bearer[2:],
			kind: model.BodyFormData, form: "title=Photo; file=/tmp/photo.png (file)"},
		{path: "Checkout", method: "POST", url: "{{baseUrl}}/checkout", headers: bearer[2:],
			kind: model.BodyURLEncoded, form: "coupon=TEA10; gift=yes (off)"},
	} {
		r := at(t, col, tc.path)
		got := []string{r.Method, r.URL, show(r.Params), show(r.Headers), string(r.Body.Kind), r.Body.Raw, show(r.Body.Form)}
		want := []string{tc.method, tc.url, tc.params, tc.headers, string(tc.kind), tc.raw, tc.form}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.path, got, want)
		}
	}

	// Rows are told apart by their IDs in the window.
	login := at(t, col, "Auth/Login")
	if login.Headers[0].ID == "" {
		t.Error("an imported row has no ID")
	}
	want := "pm.test('logged in', function () {\n    pm.response.to.have.status(200);\n});"
	if login.PostScript.Source != want || login.PostScript.Lang != "js" {
		t.Errorf("post-response script: %+v", login.PostScript)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0].Message, "bearer auth became an Authorization header") {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

// The tree shows a folder's items by their order: an import must keep the
// file's, folders among requests and folders in folders.
func TestOrder(t *testing.T) {
	col := load(t, "collection-v2.1.json").Collection
	seq := map[string]int{}
	for _, f := range col.Folders {
		seq[f.Name] = f.Seq
	}
	for _, r := range col.Requests {
		seq[r.Name] = r.Seq
	}
	auth := col.Folders[0]
	seq["Auth/Login"], seq["Auth/Tokens"] = auth.Requests[0].Seq, auth.Folders[0].Seq
	want := map[string]int{"Auth": 0, "List items": 1, "Search": 2, "Admin": 3, "Checkout": 4, "Auth/Login": 0, "Auth/Tokens": 1}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Errorf("order %v, want %v", seq, want)
	}
}

// Scripts are why people keep their Postman collections: an imported
// signing script must run here and sign as it did there.
func TestImportedScriptRuns(t *testing.T) {
	login := at(t, load(t, "collection-v2.1.json").Collection, "Auth/Login")
	scopes := &vars.Scopes{Environment: map[string]string{"secret": "s3cret"}}
	_, err := script.Exec(context.Background(), script.Run{
		Name: "pre-request.js", Script: login.PreScript, Request: login, Scopes: scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(login.Body.Raw))
	if got, want := scopes.Environment["signature"], hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("signature %q, want %q", got, want)
	}
}

// What the app cannot do must be said, by the item's name: a request that
// silently lost its auth or its body fails later with no hint why.
func TestUnsupportedIsReported(t *testing.T) {
	res := load(t, "unsupported.json")
	for _, want := range []struct{ path, text string }{
		{"Reports", `auth of type "oauth2" is not supported`},
		{"Reports", "the folder's pre-request script was left out"},
		{"Reports/Query", "a graphql body is not supported"},
		{"Feed", "the body is xml, not JSON"},
		{"User", "uses pm.sendRequest"},
		{"User", "Postman's old API"},
		{"User", `the path variable :id was replaced with its value, "42"`},
		{"Me", "basic auth became an Authorization header"},
	} {
		if !slices.ContainsFunc(res.Warnings, func(w importer.Warning) bool {
			return w.Path == want.path && strings.Contains(w.Message, want.text)
		}) {
			t.Errorf("no warning for %s with %q", want.path, want.text)
		}
	}
	if len(res.Warnings) != 8 {
		t.Errorf("%d warnings, want 8:\n%v", len(res.Warnings), res.Warnings)
	}

	col := res.Collection
	if r := at(t, col, "Reports/Query"); r.Body.Kind != model.BodyNone {
		t.Errorf("the GraphQL body became %+v", r.Body)
	}
	// Text that is not JSON is kept: the user can still send it.
	if r := at(t, col, "Feed"); r.Body.Raw != "<feed/>" {
		t.Errorf("the XML body became %+v", r.Body)
	}
	if r := at(t, col, "User"); r.URL != "https://api.example/users/42" {
		t.Errorf("URL %q", r.URL)
	}
	want := "Authorization=Basic " + base64.StdEncoding.EncodeToString([]byte("ann:pw"))
	if got := show(at(t, col, "Me").Headers); got != want {
		t.Errorf("headers %q, want %q", got, want)
	}
}

// A secret must arrive marked as one, or the window shows it in the clear.
func TestEnvironment(t *testing.T) {
	res := load(t, "environment.json")
	if res.Collection != nil || res.Globals != nil || len(res.Environments) != 1 {
		t.Fatalf("not an environment: %+v", res)
	}
	env := res.Environments[0]
	if env.Name != "Staging" {
		t.Errorf("name %q", env.Name)
	}
	want := "baseUrl=https://staging.example; apiKey=k-123 (secret); old=x (off)"
	if got := show(env.Variables); got != want {
		t.Errorf("variables %q, want %q", got, want)
	}
}

// Globals belong with the workspace's globals, not in an environment the
// user would have to choose.
func TestGlobals(t *testing.T) {
	res := load(t, "globals.json")
	if res.Environments != nil || show(res.Globals) != "tenant=acme" {
		t.Errorf("globals: %+v", res)
	}
}

// A file that is not Postman's must be refused: an empty collection in the
// tree would look like an import that worked.
func TestNotPostman(t *testing.T) {
	for name, data := range map[string]string{
		"garbage":    "not json {",
		"other JSON": `{"name": "package", "version": "1.0.0"}`,
		"a list":     `[1, 2, 3]`,
		"v1":         `{"id": "1", "name": "Old", "order": [], "requests": []}`,
	} {
		if res, err := Import([]byte(data)); err == nil {
			t.Errorf("%s: imported as %+v", name, res)
		}
	}
}

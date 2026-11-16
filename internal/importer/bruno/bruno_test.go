package bruno

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"http_gui/internal/importer"
	"http_gui/internal/model"
	"http_gui/internal/script"
	"http_gui/internal/vars"
)

func folder(t *testing.T) *importer.Result {
	t.Helper()
	res, err := ImportDir(filepath.Join("testdata", "shop"))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func export(t *testing.T) *importer.Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// at returns the request at a path such as "Orders/Create order".
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

// The parser must read what Bruno writes: a file it gets wrong is a
// request that sends something else than it did in Bruno.
func TestParse(t *testing.T) {
	src := strings.Join([]string{
		`meta {`,
		`  name: Send it`,
		`  tags: [`,
		`    smoke`,
		`  ]`,
		`  seq: 3`,
		`}`,
		``,
		`headers {`,
		`  accept: text/plain`,
		`  ~x-debug: 1`,
		`  "key with: colon": a: b`,
		`  ~"say \"hi\"": yes`,
		`  @description('the tenant: which one')`,
		`  x-tenant: acme`,
		`  empty:`,
		`  key: '''`,
		`    -----BEGIN-----`,
		`    abc`,
		`  '''`,
		`}`,
		``,
		`body:json {`,
		`  {`,
		`    "a": {"b": 1}`,
		`  }`,
		`}`,
		``,
		`script:pre-request {`,
		`  if (x) {`,
		`    y();`,
		`  }`,
		`}`,
		``,
		`vars:secret [`,
		`  token,`,
		`  ~old`,
		`]`,
		`vars:secret [one, two]`,
		`color: red`,
		``,
	}, "\n")
	for name, text := range map[string]string{"unix": src, "windows": strings.ReplaceAll(src, "\n", "\r\n")} {
		blocks, err := parse(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for _, b := range blocks {
			s := b.name
			for _, p := range b.pairs {
				s += fmt.Sprintf(" | %s=%q %v", p.Name, p.Value, p.Enabled)
			}
			if b.text != "" {
				s += " | text " + strconv.Quote(b.text)
			}
			if b.list != nil {
				s += " | list " + strings.Join(b.list, ",")
			}
			got = append(got, s)
		}
		want := []string{
			`meta | name="Send it" true | tags="" true | seq="3" true`,
			`headers | accept="text/plain" true | x-debug="1" false | key with: colon="a: b" true | say "hi"="yes" false` +
				` | x-tenant="acme" true | empty="" true | key="-----BEGIN-----\nabc" true`,
			`body:json | text "{\n  \"a\": {\"b\": 1}\n}"`,
			`script:pre-request | text "if (x) {\n  y();\n}"`,
			`vars:secret | list token,~old`,
			`vars:secret | list one,two`,
			`color | text "red"`,
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %s\nwant %s", name, strings.Join(got, "\n     "), strings.Join(want, "\n     "))
		}
	}

	// A file cut short must not import as a request with half a body.
	for _, bad := range []string{"body:json {\n  {\"a\": 1}\n", "headers {\n  no colon here\n}\n", "what is this\n"} {
		if blocks, err := parse(bad); err == nil {
			t.Errorf("%q parsed as %+v", bad, blocks)
		}
	}
}

// An imported request must send what Bruno would have sent: the same URL,
// parameters, headers and body, with what was off still off.
func TestCollection(t *testing.T) {
	col := folder(t).Collection
	if col.Name != "Shop" {
		t.Errorf("name %q", col.Name)
	}
	if got := show(col.Variables); got != "baseUrl=https://shop.example; old=x (off)" {
		t.Errorf("variables: %s", got)
	}

	const bearer = "Authorization=Bearer {{token}}"
	for _, tc := range []struct {
		path, method, url, params, headers string
		kind                               model.BodyKind
		raw, form                          string
	}{
		// Login says it has no auth: the collection's token is not for it.
		{path: "Auth/Login", method: "POST", url: "{{baseUrl}}/login", kind: model.BodyURLEncoded,
			form: "user=ann; remember=yes (off)"},
		{path: "Orders/Create order", method: "POST", url: "{{baseUrl}}/orders",
			headers: "accept=application/json; " + bearer, kind: model.BodyJSON,
			raw: "{\n  \"item\": {\"sku\": \"tea\", \"qty\": 2}\n}"},
		// The query leaves the URL: it would be sent twice otherwise.
		{path: "Orders/List orders", method: "GET", url: "{{baseUrl}}/orders", params: "page=2; q=green tea; draft=1 (off)",
			headers: "accept=application/json; x-debug=1 (off); x:odd name=yes; " + bearer, kind: model.BodyNone},
		{path: "Orders/Archive/Purge", method: "DELETE", url: "{{baseUrl}}/orders/42",
			headers: "Authorization=Basic " + base64.StdEncoding.EncodeToString([]byte("ann:pw")), kind: model.BodyNone},
		{path: "Upload", method: "POST", url: "{{baseUrl}}/upload", headers: bearer, kind: model.BodyFormData,
			form: "title=Photo; file=/tmp/photo.png (file)"},
		// Text that is not JSON is kept: the user can still send it.
		{path: "Feed", method: "PUT", url: "{{baseUrl}}/feed", headers: bearer, kind: model.BodyJSON,
			raw: "<feed>\n  <id>1</id>\n</feed>"},
	} {
		r := at(t, col, tc.path)
		got := []string{r.Method, r.URL, show(r.Params), show(r.Headers), string(r.Body.Kind), r.Body.Raw, show(r.Body.Form)}
		want := []string{tc.method, tc.url, tc.params, tc.headers, string(tc.kind), tc.raw, tc.form}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.path, got, want)
		}
	}

	// Rows are told apart by their IDs in the window.
	order := at(t, col, "Orders/Create order")
	if order.Headers[0].ID == "" {
		t.Error("an imported row has no ID")
	}
	// Bruno's tests run after its post-response script: here they are one.
	want := "bru.setEnvVar('orderId', res.body.id);\n\ntest('created', function () {\n  expect(res.getStatus()).to.equal(201);\n});"
	if order.PostScript.Source != want || order.PostScript.Lang != "js" {
		t.Errorf("post-response script: %+v", order.PostScript)
	}
}

// The tree must show the items as Bruno did: by seq, then by name, with
// only the folders that hold requests.
func TestOrder(t *testing.T) {
	var names []string
	for _, line := range outline(&folder(t).Collection.Folder, "") {
		name, _, _ := strings.Cut(line, ":")
		names = append(names, name)
	}
	want := []string{"0 Auth/", "  1 Login", "2 Orders/", "  0 Archive/", "    1 Purge", "  1 List orders", "  2 Create order", "1 Upload", "2 Feed"}
	if !slices.Equal(names, want) {
		t.Errorf("order %q, want %q", names, want)
	}
}

// Bruno has two forms of a collection, the folder and the file it exports:
// both must give the same collection.
func TestExportAgrees(t *testing.T) {
	dir, file := folder(t), export(t)
	a, b := outline(&dir.Collection.Folder, ""), outline(&file.Collection.Folder, "")
	if !slices.Equal(a, b) {
		t.Errorf("the folder and the export differ:\nfolder:\n%s\nexport:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
	if x, y := show(dir.Collection.Variables), show(file.Collection.Variables); x != y {
		t.Errorf("variables differ: %q and %q", x, y)
	}
	// The export lists its items in another order, and so its warnings.
	sorted := func(res *importer.Result) []string {
		var out []string
		for _, w := range res.Warnings {
			out = append(out, w.String())
		}
		slices.Sort(out)
		return out
	}
	if x, y := sorted(dir), sorted(file); !slices.Equal(x, y) {
		t.Errorf("warnings differ:\n%s\n%s", strings.Join(x, "\n"), strings.Join(y, "\n"))
	}
	for _, res := range []*importer.Result{dir, file} {
		if len(res.Environments) != 1 {
			t.Fatalf("%d environments", len(res.Environments))
		}
		env := res.Environments[0]
		// The secret arrives as a name: Bruno keeps its value elsewhere.
		if got := env.Name + ": " + show(env.Variables); got != "Staging: baseUrl=https://staging.example; old=x (off); apiKey= (secret)" {
			t.Errorf("environment %s", got)
		}
	}
}

// What the app cannot do must be said, by the item's name: a request that
// silently lost its auth or its body fails later with no hint why.
func TestUnsupportedIsReported(t *testing.T) {
	res := folder(t)
	for _, want := range []struct{ path, text string }{
		{"", "the collection's headers were left out"},
		{"", "the collection's pre-request script was left out"},
		{"", "bearer auth became an Authorization header of the requests inside"},
		{"Auth/Login", "the post-response variables were left out"},
		{"Auth/Login", "the docs were left out"},
		{"Orders/Archive/Purge", `the path variable :id was replaced with its value, "42"`},
		{"Orders/Archive/Purge", "basic auth became an Authorization header"},
		{"Feed", "the body is xml, not JSON"},
		{"Feed", "uses bru.sleep"},
		{"Feed", "uses require('lodash')"},
		{"environments/Staging", "secret variables come without their values: fill in apiKey"},
	} {
		if !slices.ContainsFunc(res.Warnings, func(w importer.Warning) bool {
			return w.Path == want.path && strings.Contains(w.Message, want.text)
		}) {
			t.Errorf("no warning for %q with %q", want.path, want.text)
		}
	}
	if len(res.Warnings) != 11 {
		t.Errorf("%d warnings, want 11:\n%v", len(res.Warnings), res.Warnings)
	}
}

// Scripts are why people keep their collections: an imported script must
// run here and change the request as it did in Bruno.
func TestImportedScriptRuns(t *testing.T) {
	order := at(t, folder(t).Collection, "Orders/Create order")
	scopes := &vars.Scopes{}
	out, err := script.Exec(context.Background(), script.Run{
		Name: "pre-request.js", Script: order.PreScript, Request: order, Scopes: scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(order.Body.Raw)); err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(compact.Len())
	if got := scopes.Runtime["bodyLength"]; got != want {
		t.Errorf("bru.setVar gave %q, want %q", got, want)
	}
	if !strings.Contains(show(out.Request.Headers), "X-Body-Length="+want) {
		t.Errorf("req.setHeader gave headers %q", show(out.Request.Headers))
	}
}

// The importer warns of the bru functions the script API lacks, so its
// list of the ones it has must be the script API's own.
func TestShimIsTheScriptAPIs(t *testing.T) {
	scopes := &vars.Scopes{}
	_, err := script.Exec(context.Background(), script.Run{
		Name:    "list.js",
		Script:  model.Script{Lang: "js", Source: "bru.setVar('names', Object.keys(bru).sort().join(','))"},
		Request: &model.Request{Method: "GET", URL: "http://x.example"},
		Scopes:  scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values(shim))
	if got := scopes.Runtime["names"]; got != strings.Join(want, ",") {
		t.Errorf("the script API has %s\nthe importer lists %s", got, strings.Join(want, ","))
	}
}

// A folder or a file that is not Bruno's must be refused, and say why: an
// empty collection in the tree would look like an import that worked.
func TestNotBruno(t *testing.T) {
	empty := t.TempDir()
	if res, err := ImportDir(empty); err == nil || !strings.Contains(err.Error(), "no bruno.json") {
		t.Errorf("an empty folder: %+v, %v", res, err)
	}
	yaml := t.TempDir()
	if err := os.WriteFile(filepath.Join(yaml, "opencollection.yml"), []byte("opencollection: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := ImportDir(yaml); err == nil || !strings.Contains(err.Error(), "YAML") {
		t.Errorf("a YAML collection: %+v, %v", res, err)
	}
	for name, data := range map[string]string{
		"garbage":    "not json {",
		"other JSON": `{"name": "package", "version": "1.0.0"}`,
		"Postman":    `{"info": {"name": "Shop"}, "item": []}`,
	} {
		if res, err := ImportJSON([]byte(data)); err == nil {
			t.Errorf("%s: imported as %+v", name, res)
		}
	}
}

// One file that cannot be read must not lose the rest of the collection.
func TestBrokenFileIsSkipped(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"bruno.json": `{"name": "Half"}`,
		"Good.bru":   "meta {\n  name: Good\n}\n\nget {\n  url: https://x.example\n}\n",
		"Bad.bru":    "get {\n  url: https://x.example\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ImportDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := outline(&res.Collection.Folder, ""); len(got) != 1 || !strings.Contains(got[0], "Good: GET https://x.example") {
		t.Errorf("imported %q", got)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Path != "Bad.bru" || !strings.Contains(res.Warnings[0].Message, "not closed") {
		t.Errorf("warnings %v", res.Warnings)
	}
}

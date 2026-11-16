// Package bruno reads Bruno's collections: a folder of .bru files, or the
// single JSON file Bruno exports. Both become the same values first, the
// ones the JSON has, and one mapping takes those to the model.
package bruno

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"http_gui/internal/importer"
	"http_gui/internal/model"
)

// ImportJSON reads a collection exported by Bruno as one JSON file.
func ImportJSON(data []byte) (*importer.Result, error) {
	var col collection
	if err := json.Unmarshal(data, &col); err != nil {
		return nil, fmt.Errorf("not a Bruno export: %w", err)
	}
	if col.Version == "" || col.Items == nil {
		return nil, errors.New("not a Bruno collection")
	}
	return new(conv).result(&col), nil
}

// The JSON of an exported collection.
type collection struct {
	Version      string        `json:"version"`
	Name         string        `json:"name"`
	Items        []item        `json:"items"`
	Environments []environment `json:"environments"`
	Root         *root         `json:"root"`
}

type item struct {
	Type    string   `json:"type"` // "folder", "http-request", "graphql-request" and others
	Name    string   `json:"name"`
	Seq     float64  `json:"seq"`
	Request *request `json:"request"`
	Items   []item   `json:"items"`
	Root    *root    `json:"root"` // a folder's own settings
}

// root is what a folder or the collection sets for the requests inside.
type root struct {
	Request *request `json:"request"`
	Meta    struct {
		Name string  `json:"name"`
		Seq  float64 `json:"seq"`
	} `json:"meta"`
}

type request struct {
	URL     string `json:"url"`
	Method  string `json:"method"`
	Headers []pair `json:"headers"`
	Params  []pair `json:"params"`
	Auth    auth   `json:"auth"`
	Body    body   `json:"body"`
	Script  struct {
		Req string `json:"req"`
		Res string `json:"res"`
	} `json:"script"`
	Vars struct {
		Req []pair `json:"req"`
		Res []pair `json:"res"`
	} `json:"vars"`
	Assertions []pair `json:"assertions"`
	Tests      string `json:"tests"`
	Docs       string `json:"docs"`
}

type auth struct {
	Mode   string `json:"mode"` // "none", "inherit", or the kind: "bearer", "basic", ...
	Bearer struct {
		Token string `json:"token"`
	} `json:"bearer"`
	Basic struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"basic"`
}

type body struct {
	Mode           string `json:"mode"`
	JSON           string `json:"json"`
	Text           string `json:"text"`
	XML            string `json:"xml"`
	Sparql         string `json:"sparql"`
	FormURLEncoded []pair `json:"formUrlEncoded"`
	MultipartForm  []pair `json:"multipartForm"`
}

type environment struct {
	Name      string `json:"name"`
	Variables []pair `json:"variables"`
}

// pair is an entry of any of Bruno's lists: a header, a parameter, a form
// field or a variable.
type pair struct {
	Name    string
	Value   string
	Files   []string // of a multipart field that sends files
	Enabled bool
	Type    string // "query" or "path" for a parameter, "file" for a field
	Secret  bool
}

func (p *pair) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name    string          `json:"name"`
		Value   json.RawMessage `json:"value"`
		Enabled *bool           `json:"enabled"`
		Type    string          `json:"type"`
		Secret  bool            `json:"secret"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = pair{Name: raw.Name, Enabled: raw.Enabled == nil || *raw.Enabled, Type: raw.Type, Secret: raw.Secret}
	// A value is text, a list of files, or a number or such, kept as written.
	switch {
	case len(raw.Value) == 0 || string(raw.Value) == "null":
	case json.Unmarshal(raw.Value, &p.Value) == nil:
	case json.Unmarshal(raw.Value, &p.Files) == nil:
	default:
		p.Value = string(raw.Value)
	}
	return nil
}

// header returns the Authorization header that says what the auth says.
// Only bearer and basic have one, and basic only without {{variables}},
// which would have to be encoded when the request is sent.
func (a *auth) header() (string, bool) {
	if a == nil {
		return "", false
	}
	switch a.Mode {
	case "bearer":
		return "Bearer " + a.Bearer.Token, true
	case "basic":
		pair := a.Basic.Username + ":" + a.Basic.Password
		if !strings.Contains(pair, "{{") {
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(pair)), true
		}
	}
	return "", false
}

func named(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

func rows(in []pair) []model.KV {
	var out []model.KV
	for _, p := range in {
		out = append(out, model.KV{ID: model.NewID(), Key: p.Name, Value: p.Value, Enabled: p.Enabled, Secret: p.Secret})
	}
	return out
}

// conv is one import: it collects the warnings.
type conv struct {
	warnings []importer.Warning
}

func (c *conv) warn(path, format string, args ...any) {
	c.warnings = append(c.warnings, importer.Warning{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *conv) result(in *collection) *importer.Result {
	col := &model.Collection{Folder: model.Folder{Name: named(in.Name, "Imported collection")}}
	var inherited *auth
	if in.Root != nil && in.Root.Request != nil {
		col.Variables = rows(in.Root.Request.Vars.Req)
		inherited = c.container("", "collection", in.Root.Request, nil)
	}
	c.folder(&col.Folder, in.Items, "", inherited)

	res := &importer.Result{Collection: col}
	for _, e := range in.Environments {
		env := &model.Environment{Name: named(e.Name, "Imported environment"), Variables: rows(e.Variables)}
		// Bruno keeps a secret's value out of its files.
		var empty []string
		for _, v := range e.Variables {
			if v.Secret && v.Value == "" {
				empty = append(empty, v.Name)
			}
		}
		if len(empty) > 0 {
			c.warn("environments/"+env.Name, "secret variables come without their values: fill in %s", strings.Join(empty, ", "))
		}
		res.Environments = append(res.Environments, env)
	}
	res.Warnings = c.warnings
	return res
}

// container warns of what a folder or the collection sets for its requests
// that only requests can have here, and returns the auth they inherit.
func (c *conv) container(path, kind string, r *request, inherited *auth) *auth {
	if slices.ContainsFunc(r.Headers, func(p pair) bool { return p.Enabled }) {
		c.warn(path, "the %s's headers were left out: only requests have headers", kind)
	}
	for _, s := range [][2]string{{"pre-request script was", r.Script.Req}, {"post-response script was", r.Script.Res}, {"tests were", r.Tests}} {
		if strings.TrimSpace(s[1]) != "" {
			c.warn(path, "the %s's %s left out: only requests have scripts", kind, s[0])
		}
	}
	if kind == "folder" && len(r.Vars.Req) > 0 {
		c.warn(path, "the folder's variables were left out: only collections and environments have variables")
	}
	if len(r.Vars.Res) > 0 {
		c.warn(path, "the %s's post-response variables were left out", kind)
	}
	if r.Auth.Mode == "" || r.Auth.Mode == "inherit" {
		return inherited
	}
	c.authNote(path, &r.Auth, " of the requests inside")
	return &r.Auth
}

// authNote says what became of an auth, where the collection has it.
func (c *conv) authNote(path string, a *auth, where string) {
	if a.Mode == "none" {
		return
	}
	switch _, ok := a.header(); {
	case ok:
		c.warn(path, "%s auth became an Authorization header%s", a.Mode, where)
	case a.Mode == "basic":
		c.warn(path, "basic auth with {{variables}} cannot become a header: it was left out")
	default:
		c.warn(path, "auth of type %q is not supported: it was left out", a.Mode)
	}
}

// folder fills out with items, in Bruno's order: by seq, then by name.
func (c *conv) folder(out *model.Folder, items []item, path string, inherited *auth) {
	for _, it := range items {
		p := it.Name
		if path != "" {
			p = path + "/" + it.Name
		}
		switch it.Type {
		case "folder":
			f := &model.Folder{Name: named(it.Name, "Folder"), Seq: int(it.Seq)}
			below := inherited
			if it.Root != nil {
				if f.Seq == 0 {
					f.Seq = int(it.Root.Meta.Seq)
				}
				if it.Root.Request != nil {
					below = c.container(p, "folder", it.Root.Request, inherited)
				}
			}
			c.folder(f, it.Items, p, below)
			out.Folders = append(out.Folders, f)
		case "http-request", "graphql-request":
			if it.Request == nil {
				c.warn(p, "has no request: left out")
				continue
			}
			out.Requests = append(out.Requests, c.request(p, it, inherited))
		default:
			c.warn(p, "a %s item is not supported: left out", it.Type)
		}
	}
	slices.SortStableFunc(out.Folders, func(a, b *model.Folder) int {
		return cmp.Or(cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.Name, b.Name))
	})
	slices.SortStableFunc(out.Requests, func(a, b *model.Request) int {
		return cmp.Or(cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.Name, b.Name))
	})
}

func (c *conv) request(path string, it item, inherited *auth) *model.Request {
	in := it.Request
	r := &model.Request{
		Name:    named(it.Name, "Request"),
		Seq:     int(it.Seq),
		Method:  strings.ToUpper(in.Method),
		Headers: rows(in.Headers),
		Body:    c.body(path, in.Body),
	}
	if r.Method == "" {
		r.Method = "GET"
	}

	// Bruno keeps the query in the URL and in the parameters, which also
	// have the ones turned off.
	if strings.TrimSpace(in.URL) == "" {
		c.warn(path, "has no URL")
	}
	base, params := importer.SplitQuery(in.URL)
	segments := strings.Split(base, "/")
	var query []pair
	for _, p := range in.Params {
		if p.Type != "path" {
			query = append(query, p)
			continue
		}
		// A path parameter: /users/:id, with the value beside the URL.
		name := ":" + p.Name
		for i, s := range segments {
			switch {
			case s != name:
			case p.Value == "":
				c.warn(path, "the path variable %s has no value: it is left in the URL", name)
			default:
				segments[i] = p.Value
				c.warn(path, "the path variable %s was replaced with its value, %q", name, p.Value)
			}
		}
	}
	if query != nil {
		params = rows(query)
	}
	r.URL, r.Params = strings.Join(segments, "/"), params

	// Tests run after the post-response script, so they follow it.
	post := in.Script.Res
	if strings.TrimSpace(in.Tests) != "" {
		if strings.TrimSpace(post) != "" {
			post += "\n\n"
		}
		post += in.Tests
	}
	r.PreScript = c.script(path, "pre-request", in.Script.Req)
	r.PostScript = c.script(path, "post-response", post)

	a := inherited
	if in.Auth.Mode != "" && in.Auth.Mode != "inherit" {
		a = &in.Auth
		c.authNote(path, a, "")
	}
	// A header the request already has is the one it keeps.
	has := func(kv model.KV) bool { return kv.Enabled && strings.EqualFold(kv.Key, "Authorization") }
	if value, ok := a.header(); ok && !slices.ContainsFunc(r.Headers, has) {
		r.Headers = append(r.Headers, model.KV{ID: model.NewID(), Key: "Authorization", Value: value, Enabled: true})
	}

	for _, left := range []struct {
		what string
		is   bool
	}{
		{"pre-request variables", len(in.Vars.Req) > 0},
		{"post-response variables", len(in.Vars.Res) > 0},
		{"assertions", len(in.Assertions) > 0},
		{"docs", strings.TrimSpace(in.Docs) != ""},
	} {
		if left.is {
			c.warn(path, "the %s were left out", left.what)
		}
	}
	return r
}

func (c *conv) body(path string, b body) model.Body {
	// JSON is the one kind of text body here. Other text is kept, not lost,
	// and the warning says how it will be sent.
	text := func(kind, raw string) model.Body {
		if raw == "" {
			return model.Body{Kind: model.BodyNone}
		}
		if kind != "json" {
			c.warn(path, "the body is %s, not JSON: it is kept as written, and sent as application/json unless a Content-Type header says otherwise", kind)
		}
		return model.Body{Kind: model.BodyJSON, Raw: raw}
	}
	switch b.Mode {
	case "", "none":
	case "json":
		return text("json", b.JSON)
	case "text":
		return text("text", b.Text)
	case "xml":
		return text("xml", b.XML)
	case "sparql":
		return text("sparql", b.Sparql)
	case "formUrlEncoded":
		return model.Body{Kind: model.BodyURLEncoded, Form: rows(b.FormURLEncoded)}
	case "multipartForm":
		form := rows(b.MultipartForm)
		for i, p := range b.MultipartForm {
			if p.Type != "file" && len(p.Files) == 0 {
				continue
			}
			form[i].File = true
			if len(p.Files) > 0 {
				form[i].Value = p.Files[0]
			}
			if len(p.Files) > 1 {
				c.warn(path, "the form field %q sends %d files: only the first is kept", p.Name, len(p.Files))
			}
		}
		return model.Body{Kind: model.BodyFormData, Form: form}
	default:
		c.warn(path, "a %s body is not supported: the body was left out", b.Mode)
	}
	return model.Body{Kind: model.BodyNone}
}

// What a script may call. shim lists the functions of bru that the script
// API has (internal/script/js/prelude.js); a test holds the two together.
var (
	shim = []string{
		"getEnvVar", "setEnvVar", "hasEnvVar", "deleteEnvVar", "getGlobalEnvVar", "setGlobalEnvVar",
		"getCollectionVar", "getVar", "setVar", "hasVar", "deleteVar", "interpolate",
	}
	modules  = []string{"crypto", "node:crypto", "crypto-js", "chai"}
	bruCall  = regexp.MustCompile(`\bbru\.(\w+)`)
	required = regexp.MustCompile(`\brequire\(\s*['"]([^'"]+)['"]\s*\)`)
)

// script returns the script, and warns of what it calls that is not here.
func (c *conv) script(path, name, src string) model.Script {
	if strings.TrimSpace(src) == "" {
		return model.Script{}
	}
	var missing []string
	for _, m := range bruCall.FindAllStringSubmatch(src, -1) {
		if call := "bru." + m[1]; !slices.Contains(shim, m[1]) && !slices.Contains(missing, call) {
			missing = append(missing, call)
		}
	}
	for _, m := range required.FindAllStringSubmatch(src, -1) {
		if call := "require('" + m[1] + "')"; !slices.Contains(modules, m[1]) && !slices.Contains(missing, call) {
			missing = append(missing, call)
		}
	}
	for _, call := range missing {
		c.warn(path, "the %s script uses %s, which is not supported: the script will fail there", name, call)
	}
	return model.Script{Lang: "js", Source: src}
}

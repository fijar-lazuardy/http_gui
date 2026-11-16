// Package postman reads Postman's exports: collections in the v2.0 and
// v2.1 formats, environments and globals.
package postman

import (
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

// Import reads a Postman export: a collection, an environment or globals.
// What the file is, it tells from the JSON, not from a name.
func Import(data []byte) (*importer.Result, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("not a Postman export: %w", err)
	}
	im := &conv{}
	res := &importer.Result{}
	switch {
	case f.Info != nil && f.Item != nil:
		col := &model.Collection{Folder: model.Folder{Name: named(f.Info.Name, "Imported collection")}}
		col.Variables = rows(f.Variable)
		im.container("", "collection", f.Auth, f.Event)
		im.folder(&col.Folder, f.Item, "", f.Auth)
		res.Collection = col
	case f.Info == nil && f.Requests != nil:
		return nil, errors.New("this is Postman's old v1 collection format: export the collection as v2.1")
	case f.Values != nil && f.Scope == "globals":
		res.Globals = rows(f.Values)
	case f.Values != nil:
		res.Environments = []*model.Environment{{Name: named(f.Name, "Imported environment"), Variables: rows(f.Values)}}
	default:
		return nil, errors.New("not a Postman collection or environment")
	}
	res.Warnings = im.warnings
	return res, nil
}

// The JSON of the exports. One file type covers them all: a collection has
// info and item, an environment values.
type file struct {
	Info *struct {
		Name string `json:"name"`
	} `json:"info"`
	Item     []item  `json:"item"`
	Event    []event `json:"event"`
	Variable []row   `json:"variable"`
	Auth     *auth   `json:"auth"`

	Name   string `json:"name"`
	Values []row  `json:"values"`
	Scope  string `json:"_postman_variable_scope"`

	Requests json.RawMessage `json:"requests"` // only the v1 format has it
}

// item is a folder, when it has item, or a request.
type item struct {
	Name    string   `json:"name"`
	Item    []item   `json:"item"`
	Request *request `json:"request"`
	Event   []event  `json:"event"`
	Auth    *auth    `json:"auth"`
}

type request struct {
	Method string  `json:"method"`
	URL    address `json:"url"`
	Header headers `json:"header"`
	Body   *body   `json:"body"`
	Auth   *auth   `json:"auth"`
}

// A request may be only its URL.
func (r *request) UnmarshalJSON(b []byte) error {
	if isString(b) {
		return json.Unmarshal(b, &r.URL.Raw)
	}
	type plain request
	return json.Unmarshal(b, (*plain)(r))
}

// address is a URL: an object in v2.1, an object or a string in v2.0.
type address struct {
	Raw      string `json:"raw"`
	Query    []row  `json:"query"`
	Variable []row  `json:"variable"`
}

func (a *address) UnmarshalJSON(b []byte) error {
	if isString(b) {
		return json.Unmarshal(b, &a.Raw)
	}
	type plain address
	return json.Unmarshal(b, (*plain)(a))
}

// row is an entry of any of Postman's lists: a header, a query parameter,
// a form field or a variable.
type row struct {
	Key      text            `json:"key"`
	Value    text            `json:"value"`
	Disabled bool            `json:"disabled"`
	Enabled  *bool           `json:"enabled"` // environments say it this way
	Type     string          `json:"type"`    // "file" in a form, "secret" in an environment
	Src      json.RawMessage `json:"src"`     // a form's file: a path or a list of them
}

// headers is a list of rows, or in v2.0 also "Name: value" lines.
type headers []row

func (h *headers) UnmarshalJSON(b []byte) error {
	if !isString(b) {
		return json.Unmarshal(b, (*[]row)(h))
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for line := range strings.Lines(s) {
		if k, v, ok := strings.Cut(line, ":"); ok {
			*h = append(*h, row{Key: text(strings.TrimSpace(k)), Value: text(strings.TrimSpace(v))})
		}
	}
	return nil
}

type body struct {
	Mode       string `json:"mode"`
	Raw        string `json:"raw"`
	URLEncoded []row  `json:"urlencoded"`
	FormData   []row  `json:"formdata"`
	Disabled   bool   `json:"disabled"`
	Options    struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
}

type event struct {
	Listen   string `json:"listen"` // "prerequest" or "test"
	Disabled bool   `json:"disabled"`
	Script   struct {
		Exec lines `json:"exec"`
	} `json:"script"`
}

// lines is a script: a list of its lines, or one string.
type lines string

func (l *lines) UnmarshalJSON(b []byte) error {
	var list []string
	if isString(b) {
		list = []string{""}
		if err := json.Unmarshal(b, &list[0]); err != nil {
			return err
		}
	} else if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*l = lines(strings.Join(list, "\n"))
	return nil
}

// text is a value that is usually a string. A number, a boolean or an
// object is kept as it is written; null is empty.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	switch {
	case isString(b):
		return json.Unmarshal(b, (*string)(t))
	case string(b) == "null":
		*t = ""
	default:
		*t = text(b)
	}
	return nil
}

// auth is an auth block: its type and that type's parameters, which are
// [{key, value}] in v2.1 and {key: value} in v2.0.
type auth struct {
	Type   string
	Params map[string]string
}

func (a *auth) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	json.Unmarshal(raw["type"], &a.Type) // without a type it is reported as not supported
	a.Params = map[string]string{}
	params, ok := raw[a.Type]
	if !ok {
		return nil
	}
	var list []row
	if json.Unmarshal(params, &list) == nil {
		for _, r := range list {
			a.Params[string(r.Key)] = string(r.Value)
		}
		return nil
	}
	var object map[string]text
	if err := json.Unmarshal(params, &object); err != nil {
		return fmt.Errorf("the %s auth's parameters: %w", a.Type, err)
	}
	for k, v := range object {
		a.Params[k] = string(v)
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
	switch a.Type {
	case "bearer":
		return "Bearer " + a.Params["token"], true
	case "basic":
		pair := a.Params["username"] + ":" + a.Params["password"]
		if !strings.Contains(pair, "{{") {
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(pair)), true
		}
	}
	return "", false
}

func isString(b []byte) bool { return len(b) > 0 && b[0] == '"' }

func named(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

// rows turns a Postman list into the model's.
func rows(in []row) []model.KV {
	var out []model.KV
	for _, r := range in {
		out = append(out, model.KV{
			ID:      model.NewID(),
			Key:     string(r.Key),
			Value:   string(r.Value),
			Enabled: !r.Disabled && (r.Enabled == nil || *r.Enabled),
			Secret:  r.Type == "secret",
		})
	}
	return out
}

// conv is one import: it collects the warnings.
type conv struct {
	warnings []importer.Warning
}

func (im *conv) warn(path, format string, args ...any) {
	im.warnings = append(im.warnings, importer.Warning{Path: path, Message: fmt.Sprintf(format, args...)})
}

// folder fills out with items. inherited is the auth of the nearest folder
// above that has one: Postman applies it to every request without its own.
func (im *conv) folder(out *model.Folder, items []item, path string, inherited *auth) {
	for i, it := range items {
		p := it.Name
		if path != "" {
			p = path + "/" + it.Name
		}
		switch {
		case it.Item != nil:
			f := &model.Folder{Name: named(it.Name, "Folder"), Seq: i}
			im.container(p, "folder", it.Auth, it.Event)
			below := inherited
			if it.Auth != nil {
				below = it.Auth
			}
			im.folder(f, it.Item, p, below)
			out.Folders = append(out.Folders, f)
		case it.Request != nil:
			r := im.request(p, it, inherited)
			r.Seq = i
			out.Requests = append(out.Requests, r)
		default:
			im.warn(p, "is neither a folder nor a request: left out")
		}
	}
}

// container warns of what a folder or the collection has that only
// requests can have here.
func (im *conv) container(path, kind string, a *auth, events []event) {
	im.authNote(path, a, " of the requests inside")
	for _, e := range events {
		if !e.Disabled && strings.TrimSpace(string(e.Script.Exec)) != "" {
			im.warn(path, "the %s's %s script was left out: only requests have scripts", kind, scriptName(e.Listen))
		}
	}
}

// authNote says what became of an auth block, where the file has it.
func (im *conv) authNote(path string, a *auth, where string) {
	if a == nil || a.Type == "noauth" {
		return
	}
	switch _, ok := a.header(); {
	case ok:
		im.warn(path, "%s auth became an Authorization header%s", a.Type, where)
	case a.Type == "basic":
		im.warn(path, "basic auth with {{variables}} cannot become a header: it was left out")
	default:
		im.warn(path, "auth of type %q is not supported: it was left out", a.Type)
	}
}

func (im *conv) request(path string, it item, inherited *auth) *model.Request {
	in := it.Request
	r := &model.Request{
		Name:    named(it.Name, "Request"),
		Method:  strings.ToUpper(in.Method),
		Headers: rows(in.Header),
		Body:    im.body(path, in.Body),
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	r.URL, r.Params = im.url(path, in.URL)
	r.PreScript, r.PostScript = im.scripts(path, it.Event)

	a := inherited
	if in.Auth != nil {
		a = in.Auth
		im.authNote(path, a, "")
	}
	// A header the request already has is the one it keeps.
	has := func(kv model.KV) bool { return kv.Enabled && strings.EqualFold(kv.Key, "Authorization") }
	if value, ok := a.header(); ok && !slices.ContainsFunc(r.Headers, has) {
		r.Headers = append(r.Headers, model.KV{ID: model.NewID(), Key: "Authorization", Value: value, Enabled: true})
	}
	return r
}

// url returns the URL without its query, and the query as parameters.
func (im *conv) url(path string, a address) (string, []model.KV) {
	if strings.TrimSpace(a.Raw) == "" {
		im.warn(path, "has no URL")
	}
	base, params := importer.SplitQuery(a.Raw)

	// Path variables: /users/:id, with the value beside the URL.
	segments := strings.Split(base, "/")
	for _, v := range a.Variable {
		name := ":" + string(v.Key)
		for i, s := range segments {
			switch {
			case s != name:
			case v.Value == "":
				im.warn(path, "the path variable %s has no value: it is left in the URL", name)
			default:
				segments[i] = string(v.Value)
				im.warn(path, "the path variable %s was replaced with its value, %q", name, v.Value)
			}
		}
	}
	base = strings.Join(segments, "/")

	// The file's own list of parameters also has the ones turned off.
	if a.Query != nil {
		params = rows(a.Query)
	}
	return base, params
}

func (im *conv) body(path string, b *body) model.Body {
	none := model.Body{Kind: model.BodyNone}
	if b == nil || b.Disabled {
		return none
	}
	switch b.Mode {
	case "", "none":
		return none
	case "raw":
		if b.Raw == "" {
			return none
		}
		// JSON is the one kind of text body here. Other text is kept, not
		// lost, and the warning says how it will be sent.
		lang := b.Options.Raw.Language
		start := strings.TrimSpace(b.Raw)
		looksJSON := strings.HasPrefix(start, "{") || strings.HasPrefix(start, "[")
		if lang != "json" && (lang != "" || !looksJSON) {
			im.warn(path, "the body is %s, not JSON: it is kept as written, and sent as application/json unless a Content-Type header says otherwise", named(lang, "text"))
		}
		return model.Body{Kind: model.BodyJSON, Raw: b.Raw}
	case "urlencoded":
		return model.Body{Kind: model.BodyURLEncoded, Form: rows(b.URLEncoded)}
	case "formdata":
		form := rows(b.FormData)
		for i, r := range b.FormData {
			if r.Type == "file" {
				form[i].File, form[i].Value = true, im.file(path, r)
			}
		}
		return model.Body{Kind: model.BodyFormData, Form: form}
	}
	im.warn(path, "a %s body is not supported: the body was left out", b.Mode)
	return none
}

// file returns the path of a form field's file: src is one path, a list of
// them, or null.
func (im *conv) file(path string, r row) string {
	var one string
	if json.Unmarshal(r.Src, &one) == nil {
		return one
	}
	var many []string
	json.Unmarshal(r.Src, &many) // null or not a list: no file chosen
	if len(many) > 1 {
		im.warn(path, "the form field %q sends %d files: only the first is kept", r.Key, len(many))
	}
	if len(many) == 0 {
		return ""
	}
	return many[0]
}

// What scripts may call in Postman that the script API here lacks.
var (
	missingCalls = []string{"pm.sendRequest", "pm.cookies", "pm.iterationData", "pm.execution", "pm.visualizer"}
	legacyCall   = regexp.MustCompile(`\bpostman\.\w+\(|\btests\[`)
)

func (im *conv) scripts(path string, events []event) (pre, post model.Script) {
	for _, e := range events {
		src := string(e.Script.Exec)
		if e.Disabled || strings.TrimSpace(src) == "" {
			continue
		}
		var into *model.Script
		switch e.Listen {
		case "prerequest":
			into = &pre
		case "test":
			into = &post
		default:
			im.warn(path, "a script for %q was left out: scripts run before the request or after the response", e.Listen)
			continue
		}
		if into.Source != "" {
			into.Source += "\n"
		}
		*into = model.Script{Lang: "js", Source: into.Source + src}

		for _, call := range missingCalls {
			if strings.Contains(src, call) {
				im.warn(path, "the %s script uses %s, which is not supported: the script will fail there", scriptName(e.Listen), call)
			}
		}
		if legacyCall.MatchString(src) {
			im.warn(path, "the %s script uses Postman's old API (postman.*, tests[]), which is not supported: use pm.*", scriptName(e.Listen))
		}
	}
	return pre, post
}

func scriptName(listen string) string {
	if listen == "test" {
		return "post-response"
	}
	return "pre-request"
}

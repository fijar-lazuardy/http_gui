package app

import (
	"bytes"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"http_gui/internal/model"
	"http_gui/internal/vars"
)

var (
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	bodyKinds = []model.BodyKind{model.BodyNone, model.BodyJSON, model.BodyFormData, model.BodyURLEncoded}
	languages = []string{"JavaScript", "TypeScript"}
)

func (a *App) main(c *ui.Context) {
	t := c.Theme()
	if a.open == nil {
		ui.Column(c).Fill().Center().Gap(t.Space(2)).Children(func() {
			ui.Text(c, "No request open").FontSize(18).Bold()
			ui.Text(c, "Choose one in the sidebar, or right-click a collection to add one.").TextColor(t.TextMuted)
		})
		return
	}
	ui.SplitVerticalPercent(c, &a.splitPercent, func() { a.requestPane(c) }, func() { a.responsePane(c) }).SplitReset(50).Fill()
}

func (a *App) requestPane(c *ui.Context) {
	t := c.Theme()
	r := a.open
	ui.Column(c).Fill().Padding(t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			method := r.Method
			if method == "" {
				method = "GET"
			}
			before := method
			ui.Select(c, &method, methods).Label("Method").Width(112)
			if method != before {
				r.Method = method
				a.requestChanged(r)
			}

			text := a.urlText
			submitted := ui.TextInput(c, &a.urlText).Placeholder("https://example.com/{{path}}").Label("URL").
				Font("monospace").Grow(1).Submitted()
			if a.urlText != text {
				setURL(r, a.urlText)
				a.requestChanged(r)
			}
			if a.sending {
				if ui.PrimaryButton(c, "Cancel").Clicked() {
					a.stop()
				}
			} else if ui.PrimaryButton(c, "Send").Clicked() || submitted {
				a.send()
			}
		})
		if missing := a.missingVars(); len(missing) > 0 {
			ui.Text(c, "No value for "+braces(missing)).TextColor(t.Warning)
		}

		ui.Tabs(c, &a.reqTab, "Params", "Headers", "Body", "Scripts")
		switch a.reqTab {
		case 0:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				if a.kvEditor(c, &r.Params, kvOptions{list: "params"}) {
					a.urlText = displayURL(r)
					a.requestChanged(r)
				}
			})
		case 1:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				if a.kvEditor(c, &r.Headers, kvOptions{list: "headers"}) {
					a.requestChanged(r)
				}
			})
		case 2:
			a.bodyTab(c, r)
		case 3:
			a.scriptsTab(c, r)
		}
	})
	a.syncEditors()
}

func (a *App) bodyTab(c *ui.Context, r *model.Request) {
	t := c.Theme()
	kind := max(slices.Index(bodyKinds, r.Body.Kind), 0)
	before := kind
	ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Segmented(c, &kind, "None", "JSON", "Form", "URL-encoded").Label("Body")
		if kind != before {
			r.Body.Kind = bodyKinds[kind]
			a.requestChanged(r)
		}
		if r.Body.Kind == model.BodyJSON {
			ui.Spacer(c)
			if a.jsonErr != "" {
				ui.Text(c, a.jsonErr).TextColor(t.TextMuted).SingleLine().Shrink(1)
			}
			if ui.Button(c, "Format").Disabled(a.jsonErr != "").Clicked() {
				var buf bytes.Buffer
				if json.Indent(&buf, []byte(r.Body.Raw), "", "  ") == nil {
					a.body.Buffer.Set(buf.String())
				}
			}
		}
	})
	switch r.Body.Kind {
	case model.BodyJSON:
		editor.View(c.Key("body"), a.body).Grow(1).FillWidth()
	case model.BodyFormData:
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if a.kvEditor(c, &r.Body.Form, kvOptions{list: "form", files: true, chooseFile: a.pickFile}) {
				a.requestChanged(r)
			}
		})
	case model.BodyURLEncoded:
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if a.kvEditor(c, &r.Body.Form, kvOptions{list: "urlencoded"}) {
				a.requestChanged(r)
			}
		})
	default:
		ui.Text(c, "This request has no body.").TextColor(t.TextMuted)
	}
}

func (a *App) scriptsTab(c *ui.Context, r *model.Request) {
	t := c.Theme()
	s, st := &r.PreScript, a.pre
	if a.scriptTab == 1 {
		s, st = &r.PostScript, a.post
	}
	ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Segmented(c, &a.scriptTab, "Pre-request", "Post-response").Label("Script")
		ui.Spacer(c)
		lang := languages[0]
		if s.Lang == "ts" {
			lang = languages[1]
		}
		before := lang
		ui.Select(c, &lang, languages).Label("Language").Width(140)
		if lang != before {
			s.Lang = scriptLang(lang)
			st.Buffer.SetLanguage(editor.LanguageNamed(lang))
			a.requestChanged(r)
		}
	})
	editor.View(c.Key(a.scriptTab), st).Grow(1).FillWidth()
}

// loadEditors makes the editors of the request just opened.
func (a *App) loadEditors() {
	r := a.open
	a.urlText = displayURL(r)
	a.body = &editor.State{Buffer: editor.NewBuffer(r.Body.Raw), Label: "JSON body", Placeholder: `{"name": "{{name}}"}`}
	a.body.Buffer.SetLanguage(editor.LanguageNamed("json"))
	a.pre = scriptEditor(r.PreScript, "Pre-request script", "pm.environment.set('now', Date.now())")
	a.post = scriptEditor(r.PostScript, "Post-response script", "pm.test('is OK', () => pm.response.to.have.status(200))")
	a.versions = [3]uint64{a.body.Buffer.Version(), a.pre.Buffer.Version(), a.post.Buffer.Version()}
	a.jsonErr = jsonError(r.Body.Raw)
}

func scriptEditor(s model.Script, label, placeholder string) *editor.State {
	st := &editor.State{Buffer: editor.NewBuffer(s.Source), Label: label, Placeholder: placeholder}
	lang := languages[0]
	if s.Lang == "ts" {
		lang = languages[1]
	}
	st.Buffer.SetLanguage(editor.LanguageNamed(lang))
	return st
}

// syncEditors copies what was typed in the editors to the request.
func (a *App) syncEditors() {
	r := a.open
	for i, st := range []*editor.State{a.body, a.pre, a.post} {
		v := st.Buffer.Version()
		if v == a.versions[i] {
			continue
		}
		a.versions[i] = v
		text := st.Buffer.String()
		switch i {
		case 0:
			r.Body.Raw = text
			a.jsonErr = jsonError(text)
		case 1:
			r.PreScript.Source = text
		case 2:
			r.PostScript.Source = text
		}
		for _, s := range []*model.Script{&r.PreScript, &r.PostScript} {
			if s.Lang == "" && s.Source != "" {
				s.Lang = "js"
			}
		}
		a.requestChanged(r)
	}
}

// missingVars returns the {{names}} of the open request that have no
// value, computed again only after an edit.
func (a *App) missingVars() []string {
	key := [3]any{a.edits, a.envID, a.open}
	if key != a.missingAt {
		a.missingAt = key
		a.missing = nil
		if it, ok := a.find(a.open.ID); ok {
			_, a.missing = vars.ResolveRequest(a.open, a.scopes(it.col))
		}
	}
	return a.missing
}

// pickFile asks for the file of a multipart row, off the main thread,
// since the dialog waits for the user.
func (a *App) pickFile(id string) {
	r := a.open
	go func() {
		paths, err := a.chooseFile()
		a.update(func() {
			if err != nil || len(paths) == 0 {
				a.fail(err)
				return
			}
			for i := range r.Body.Form {
				if r.Body.Form[i].ID == id {
					r.Body.Form[i].Value = paths[0]
					a.requestChanged(r)
				}
			}
		})
	}()
}

func jsonError(text string) string {
	if strings.TrimSpace(text) == "" || json.Valid([]byte(text)) {
		return ""
	}
	return "Not valid JSON (it can still be sent)"
}

func scriptLang(name string) string {
	if name == "TypeScript" {
		return "ts"
	}
	return "js"
}

func braces(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "{{" + n + "}}"
	}
	return strings.Join(out, ", ")
}

// displayURL is the text of the URL field: the request's URL with the
// enabled parameters after it, as they are written, not escaped.
func displayURL(r *model.Request) string {
	var q []string
	for _, kv := range r.Params {
		if inURL(kv) {
			q = append(q, kv.Key+"="+kv.Value)
		}
	}
	if len(q) == 0 {
		return r.URL
	}
	sep := "?"
	if strings.Contains(r.URL, "?") {
		sep = "&"
	}
	return r.URL + sep + strings.Join(q, "&")
}

// setURL takes the text of the URL field into r: before the "?" is the
// URL, and the query after it the enabled parameters, which keep the IDs
// of the rows they replace. Rows switched off stay as they are.
func setURL(r *model.Request, text string) {
	base, query, _ := strings.Cut(text, "?")
	r.URL = base
	var parsed []model.KV
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		parsed = append(parsed, model.KV{Key: unescape(k), Value: unescape(v), Enabled: true})
	}
	var out []model.KV
	for _, kv := range r.Params {
		if inURL(kv) {
			if len(parsed) == 0 {
				continue
			}
			kv.Key, kv.Value = parsed[0].Key, parsed[0].Value
			parsed = parsed[1:]
		}
		out = append(out, kv)
	}
	for _, kv := range parsed {
		kv.ID = model.NewID()
		out = append(out, kv)
	}
	r.Params = out
}

// inURL reports whether a parameter shows in the URL field: httpx sends
// the enabled ones that have a key.
func inURL(kv model.KV) bool { return kv.Enabled && kv.Key != "" }

func unescape(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

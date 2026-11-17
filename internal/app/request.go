package app

import (
	"bytes"
	"encoding/json"
	"maps"
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
	a.varTarget = nil
	ui.Column(c).Fill().Padding(t.Space(3)).Gap(t.Space(2)).Children(func() {
		var urlEl ui.Element
		var varAnchor ui.Element
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
			urlEl = ui.TextInput(c, &a.urlText).Placeholder("https://example.com/{{path}}").Label("URL").
				Font("monospace").Grow(1)
			submitted := urlEl.Submitted()
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
		if a.offerVarCompletion(&a.urlText, func(newText string) {
			a.urlText = newText
			setURL(r, a.urlText)
			a.requestChanged(r)
		}) {
			varAnchor = urlEl
		}
		if missing := a.missingVars(); len(missing) > 0 {
			ui.Text(c, "No value for "+braces(missing)).TextColor(t.Warning)
		}

		reqChanged := func() { a.requestChanged(r) }
		setAnchor := func(el ui.Element) { varAnchor = el }
		ui.Tabs(c, &a.reqTab, "Params", "Headers", "Body", "Scripts")
		switch a.reqTab {
		case 0:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				if a.kvEditor(c, &r.Params, kvOptions{list: "params", varChanged: func() {
					a.urlText = displayURL(r)
					a.requestChanged(r)
				}, varAnchor: setAnchor}) {
					a.urlText = displayURL(r)
					a.requestChanged(r)
				}
			})
		case 1:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				if a.kvEditor(c, &r.Headers, kvOptions{list: "headers", varChanged: reqChanged, varAnchor: setAnchor}) {
					a.requestChanged(r)
				}
			})
		case 2:
			a.bodyTab(c, r, setAnchor)
		case 3:
			a.scriptsTab(c, r)
		}
		a.renderVarPopover(c, varAnchor)
	})
	a.syncEditors()
}

func (a *App) bodyTab(c *ui.Context, r *model.Request, setAnchor func(ui.Element)) {
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
		reqChanged := func() { a.requestChanged(r) }
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if a.kvEditor(c, &r.Body.Form, kvOptions{list: "form", files: true, chooseFile: a.pickFile, varChanged: reqChanged, varAnchor: setAnchor}) {
				a.requestChanged(r)
			}
		})
	case model.BodyURLEncoded:
		reqChanged := func() { a.requestChanged(r) }
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if a.kvEditor(c, &r.Body.Form, kvOptions{list: "urlencoded", varChanged: reqChanged, varAnchor: setAnchor}) {
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

	for _, st := range []*editor.State{a.body, a.pre, a.post} {
		st.OnCompletion = a.editorVarCompletion(st)
	}
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
		a.checkVarCompletion(st)
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

// unclosedVar returns the partial name and its position after the last
// unclosed {{ in text, or -1 if every {{ is closed.
func unclosedVar(text string) (partial string, pos int) {
	last := strings.LastIndex(text, "{{")
	if last < 0 {
		return "", -1
	}
	after := text[last+2:]
	if strings.Contains(after, "}}") {
		return "", -1
	}
	return after, last + 2
}

// varInputTarget tracks the text and apply callback for the variable
// suggestion popover, without storing a ui.Element (which must not live
// in a struct).
type varInputTarget struct {
	text  *string
	pos   int          // position after {{ in *text
	apply func(string) // sets the text and handles side effects
}

// offerVarCompletion registers a TextInput as a candidate for the variable
// suggestion popover. The last candidate with an unclosed {{ wins.
// Returns true when it registered, so the caller can track the anchor element.
func (a *App) offerVarCompletion(text *string, apply func(string)) bool {
	_, pos := unclosedVar(*text)
	if pos < 0 {
		return false
	}
	a.varTarget = &varInputTarget{text, pos, apply}
	return true
}

// renderVarPopover shows the variable suggestion popover anchored to el
// for whichever TextInput was registered by offerVarCompletion this frame.
func (a *App) renderVarPopover(c *ui.Context, el ui.Element) {
	tgt := a.varTarget
	if tgt == nil {
		a.varPopup = false
		a.varDismissPtr = nil
		a.varDismissText = ""
		return
	}
	partial, _ := unclosedVar(*tgt.text)
	suggestions := a.varSuggestions(partial)
	if len(suggestions) == 0 {
		a.varPopup = false
		return
	}
	if !a.varPopup && (tgt.text != a.varDismissPtr || *tgt.text != a.varDismissText) {
		a.varPopup = true
	}
	if !a.varPopup {
		return
	}
	t := c.Theme()
	pop := ui.Popover(c, el, &a.varPopup, func() {
		ui.Scroll(c).MaxHeight(200).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, name := range suggestions {
					c.Key(name)
					label := "{{" + name + "}}"
					text := tgt.text
					pos := tgt.pos
					apply := tgt.apply
					btn := ui.Button(c, label).FillWidth().FontSize(13).Font("monospace")
					btn.OnClick(func() {
						newText := (*text)[:pos] + name + "}}"
						apply(newText)
						a.varPopup = false
					})
					_ = btn
				}
			})
		})
	}).Width(300).Padding(t.Space(1))
	if pop.Dismissed() {
		a.varDismissPtr = tgt.text
		a.varDismissText = *tgt.text
	}
}

// editorVarCompletion returns an OnCompletion callback for an editor that
// shows {{variable}} suggestions when the caret is after an unclosed {{.
func (a *App) editorVarCompletion(st *editor.State) func(editor.CompletionRequest) {
	return func(req editor.CompletionRequest) {
		text := st.Buffer.String()
		if req.Offset > len(text) {
			return
		}
		partial, varPos := unclosedVar(text[:req.Offset])
		if varPos < 0 {
			return
		}
		names := a.varSuggestions(partial)
		if len(names) == 0 {
			st.ShowCompletions(varPos, nil)
			return
		}
		items := make([]editor.Completion, len(names))
		for i, name := range names {
			items[i] = editor.Completion{Label: name, Insert: name + "}}", Kind: editor.CompletionVariable}
		}
		st.ShowCompletions(varPos, items)
	}
}

// checkVarCompletion triggers variable completions when {{ was just typed
// in an editor. The editor's own completion system only fires for word
// characters; { is not one, so this bridges the gap.
func (a *App) checkVarCompletion(st *editor.State) {
	text := st.Buffer.String()
	pos := st.Cursor()
	if pos < 2 || pos > len(text) || text[pos-2:pos] != "{{" {
		return
	}
	names := a.varSuggestions("")
	if len(names) == 0 {
		return
	}
	items := make([]editor.Completion, len(names))
	for i, name := range names {
		items[i] = editor.Completion{Label: name, Insert: name + "}}", Kind: editor.CompletionVariable}
	}
	st.ShowCompletions(pos, items)
}

// varSuggestions returns variable names matching partial, from all scopes.
func (a *App) varSuggestions(partial string) []string {
	names := map[string]bool{}
	if e := a.environment(); e != nil {
		for _, kv := range e.Variables {
			if kv.Enabled && kv.Key != "" {
				names[kv.Key] = true
			}
		}
	}
	if it, ok := a.find(a.open.ID); ok {
		for _, kv := range it.col.Variables {
			if kv.Enabled && kv.Key != "" {
				names[kv.Key] = true
			}
		}
	}
	for _, kv := range a.ws.Globals {
		if kv.Enabled && kv.Key != "" {
			names[kv.Key] = true
		}
	}
	for k := range a.runtime {
		names[k] = true
	}
	for _, d := range []string{"$guid", "$randomUUID", "$timestamp", "$isoTimestamp", "$randomInt"} {
		names[d] = true
	}
	all := slices.Sorted(maps.Keys(names))
	if partial == "" {
		return all
	}
	lower := strings.ToLower(partial)
	var out []string
	for _, name := range all {
		if strings.Contains(strings.ToLower(name), lower) {
			out = append(out, name)
		}
	}
	return out
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"http_gui/internal/httpx"
	"http_gui/internal/model"
	"http_gui/internal/runner"
	"http_gui/internal/script"
	"http_gui/internal/vars"
)

// send runs the open request. The run works on copies made here, on the
// main thread, and reports back through a.update.
func (a *App) send() {
	it, ok := a.find(a.open.ID)
	if !ok {
		return
	}
	a.stop()
	ctx, cancel := context.WithCancel(context.Background())
	run, env := a.run, a.environment()
	a.cancel, a.sending, a.result = cancel, true, nil
	in := runner.Input{
		Request: cloneRequest(a.open),
		Scopes:  a.scopes(it.col),
		Options: httpx.Options{FollowRedirects: true},
	}
	go func() {
		res := runner.Run(ctx, in)
		cancel()
		a.update(func() { a.finish(run, it.col, env, res) })
	}()
}

// stop cancels the run in flight, whose result is then dropped.
func (a *App) stop() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.sending = false
	a.run++
}

// finish shows the result of a run, and saves the variables its scripts
// changed. A run canceled or replaced since it started changes nothing.
func (a *App) finish(run int, col *model.Collection, env *model.Environment, res runner.Result) {
	if run != a.run {
		return
	}
	a.cancel, a.sending, a.result = nil, false, &res
	a.raw = false
	a.loadResponse()

	a.runtime = res.Scopes.Runtime
	if rows, changed := vars.Apply(a.ws.Globals, res.Scopes.Global); changed {
		a.ws.Globals = rows
		a.fail(a.ws.SaveGlobals())
	}
	if env != nil && slices.Contains(a.ws.Environments, env) {
		if rows, changed := vars.Apply(env.Variables, res.Scopes.Environment); changed {
			env.Variables = rows
			a.fail(a.ws.SaveEnvironment(env))
		}
	}
	if _, ok := a.find(col.ID); ok {
		if rows, changed := vars.Apply(col.Variables, res.Scopes.Collection); changed {
			col.Variables = rows
			a.fail(a.ws.SaveCollection(col))
		}
	}
	a.edits++ // the variables may have changed
}

// scopes returns copies of the variables a request of col sees.
func (a *App) scopes(col *model.Collection) *vars.Scopes {
	s := &vars.Scopes{
		Runtime:    maps.Clone(a.runtime),
		Collection: vars.Map(col.Variables),
		Global:     vars.Map(a.ws.Globals),
	}
	if s.Runtime == nil {
		s.Runtime = map[string]string{}
	}
	if e := a.environment(); e != nil {
		s.Environment = vars.Map(e.Variables)
	}
	return s
}

// loadResponse puts the response's body in the response editor.
func (a *App) loadResponse() {
	resp := a.result.Response
	if resp == nil {
		a.response = nil
		return
	}
	lang := responseLanguage(resp.Headers.Get("Content-Type"))
	text := string(resp.Body)
	if !a.raw && lang != nil && lang.Name == "JSON" {
		text = formatJSON(resp.Body)
	}
	buf := editor.NewBuffer(text)
	buf.SetLanguage(lang)
	a.response = &editor.State{Buffer: buf, Label: "Response body", ReadOnly: true, SoftWrap: true}
}

func (a *App) responsePane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(t.Space(3)).Gap(t.Space(2)).Children(func() {
		switch {
		case a.sending:
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Sending…").TextColor(t.TextMuted)
			})
		case a.result == nil:
			ui.Text(c, "Send the request to see its response.").TextColor(t.TextMuted)
		case a.result.Response == nil:
			a.failedRun(c)
		default:
			a.responseView(c)
		}
	})
}

// failedRun shows a run that got no response: why, and what its script
// logged.
func (a *App) failedRun(c *ui.Context) {
	t := c.Theme()
	res := a.result
	for _, err := range []error{res.Err, res.ScriptErr} {
		if err != nil {
			ui.Text(c, err.Error()).TextColor(t.Danger).Selectable()
		}
	}
	if len(res.Console) > 0 {
		a.consoleTab(c)
	}
}

func (a *App) responseView(c *ui.Context) {
	t := c.Theme()
	res := a.result
	resp := res.Response
	ui.Row(c).Gap(t.Space(4)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, resp.Status).Bold().TextColor(statusColor(t, resp.StatusCode))
		ui.Text(c, fmt.Sprintf("%d ms", resp.Duration.Milliseconds())).TextColor(t.TextMuted)
		ui.Text(c, size(resp.Size)).TextColor(t.TextMuted)
	})
	if res.ScriptErr != nil {
		ui.Text(c, res.ScriptErr.Error()).TextColor(t.Danger).Selectable()
	}
	ui.Tabs(c, &a.respTab, "Body", "Headers", "Console", testsLabel(res.Tests))
	switch a.respTab {
	case 0:
		if lang := a.response.Buffer.Language(); lang != nil && lang.Name == "JSON" {
			raw := a.raw
			ui.Checkbox(c, &a.raw, "Raw")
			if raw != a.raw {
				a.loadResponse()
			}
		}
		editor.View(c.Key(a.response), a.response).Grow(1).FillWidth()
	case 1:
		rows := headerRows(resp)
		cols := []ui.TableColumn{{Title: "Name", Width: 220}, {Title: "Value"}}
		ui.Table(c, &a.respHeader, cols, len(rows), func(row, col int) {
			ui.Text(c, rows[row][col]).Selectable()
		}).Grow(1).FillWidth()
	case 2:
		a.consoleTab(c)
	case 3:
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(t.Space(1)).Children(func() {
				if len(res.Tests) == 0 {
					ui.Text(c, "No tests ran. pm.test in a script adds one.").TextColor(t.TextMuted)
				}
				for _, test := range res.Tests {
					if test.Passed {
						ui.Text(c, "✓ "+test.Name).TextColor(t.Success)
					} else {
						ui.Text(c, "✗ "+test.Name+": "+test.Error).TextColor(t.Danger).Selectable()
					}
				}
			})
		})
	}
}

func (a *App) consoleTab(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(t.Space(0.5)).Selectable().Children(func() {
			if len(a.result.Console) == 0 {
				ui.Text(c, "Nothing logged. console.log in a script writes here.").TextColor(t.TextMuted)
			}
			for _, line := range a.result.Console {
				text := ui.Text(c, line.Text).Font("monospace")
				switch line.Level {
				case "error":
					text.TextColor(t.Danger)
				case "warn":
					text.TextColor(t.Warning)
				}
			}
		})
	})
}

func testsLabel(tests []script.TestResult) string {
	if len(tests) == 0 {
		return "Tests"
	}
	passed := 0
	for _, t := range tests {
		if t.Passed {
			passed++
		}
	}
	return fmt.Sprintf("Tests %d/%d", passed, len(tests))
}

// headerRows returns the response's headers as name and value, a row for
// each value, sorted by name.
func headerRows(resp *httpx.Response) [][2]string {
	var rows [][2]string
	for _, name := range slices.Sorted(maps.Keys(resp.Headers)) {
		for _, v := range resp.Headers[name] {
			rows = append(rows, [2]string{name, v})
		}
	}
	return rows
}

// responseLanguage is the editor's language for a Content-Type, nil for
// plain text.
func responseLanguage(contentType string) *editor.Language {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil
	}
	switch {
	case strings.HasSuffix(mt, "json"):
		return editor.LanguageNamed("json")
	case strings.HasSuffix(mt, "html"):
		return editor.LanguageNamed("html")
	case strings.HasSuffix(mt, "xml"):
		return editor.LanguageNamed("xml")
	case strings.HasSuffix(mt, "javascript"):
		return editor.LanguageNamed("javascript")
	case mt == "text/css":
		return editor.LanguageNamed("css")
	case strings.HasSuffix(mt, "yaml"):
		return editor.LanguageNamed("yaml")
	}
	return nil
}

// formatJSON indents JSON and decodes unicode escapes like & to their
// characters, so that URLs and text are readable and copyable.
func formatJSON(data []byte) string {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return string(data)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(v) != nil {
		return string(data)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func statusColor(t *ui.Theme, code int) ui.Color {
	switch {
	case code >= 500:
		return t.Danger
	case code >= 400:
		return t.Warning
	case code >= 300:
		return t.Accent
	}
	return t.Success
}

func size(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

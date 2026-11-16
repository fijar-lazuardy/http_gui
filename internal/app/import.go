package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"http_gui/internal/importer"
	"http_gui/internal/importer/bruno"
	"http_gui/internal/importer/postman"
	"http_gui/internal/model"
)

// The Import menu.
const (
	importFiles  = "Postman or Bruno files…"
	importFolder = "Bruno collection folder…"
)

// importReport is what became of one imported file or folder.
type importReport struct {
	title string
	notes []string
}

// pickImport asks for files or a folder, imports each, and shows what
// became of them.
func (a *App) pickImport(choose func() ([]string, error), one func(path string) importReport) {
	go func() {
		paths, err := choose()
		a.update(func() {
			a.fail(err)
			if len(paths) == 0 {
				return
			}
			a.imported = nil
			for _, path := range paths {
				a.imported = append(a.imported, one(path))
			}
			a.reporting = true
		})
	}()
}

// importFile imports a JSON file: one of Postman's exports, or a
// collection exported by Bruno.
func (a *App) importFile(path string) importReport {
	data, err := os.ReadFile(path)
	var res *importer.Result
	if err == nil {
		if res, err = postman.Import(data); err != nil {
			var other error
			if res, other = bruno.ImportJSON(data); other == nil {
				err = nil
			} else if json.Valid(data) {
				err = fmt.Errorf("%v; %v", err, other)
			}
		}
	}
	return a.add(filepath.Base(path), res, err)
}

// importFolder imports a Bruno collection kept as a folder.
func (a *App) importFolder(dir string) importReport {
	res, err := bruno.ImportDir(dir)
	return a.add(filepath.Base(dir), res, err)
}

// add adds what an import gave to the workspace. Nothing there is written
// over: a name that is taken gets a number.
func (a *App) add(file string, res *importer.Result, err error) importReport {
	if err != nil {
		return importReport{title: file + " was not imported", notes: []string{err.Error()}}
	}
	var rep importReport
	var parts []string
	if col := res.Collection; col != nil {
		col.Name = freeName(col.Name, func(name string) bool {
			return slices.ContainsFunc(a.ws.Collections, func(c *model.Collection) bool { return c.Name == name })
		})
		col.Seq = len(a.ws.Collections)
		parts = append(parts, fmt.Sprintf("collection “%s”, %s", col.Name, count(countRequests(&col.Folder), "request")))
		if err = a.ws.SaveCollection(col); err == nil {
			a.ws.Collections = append(a.ws.Collections, col)
			err = a.saveChildren(&col.Folder)
		}
	}
	for _, env := range res.Environments {
		env.Name = freeName(env.Name, func(name string) bool {
			return slices.ContainsFunc(a.ws.Environments, func(e *model.Environment) bool { return e.Name == name })
		})
		parts = append(parts, fmt.Sprintf("environment “%s”, %s", env.Name, count(len(env.Variables), "variable")))
		if err == nil {
			if err = a.ws.SaveEnvironment(env); err == nil {
				a.ws.Environments = append(a.ws.Environments, env)
			}
		}
	}
	if res.Globals != nil {
		// Globals join the workspace's; one it already has keeps its value.
		added := 0
		for _, kv := range res.Globals {
			if slices.ContainsFunc(a.ws.Globals, func(g model.KV) bool { return g.Key == kv.Key }) {
				rep.notes = append(rep.notes, fmt.Sprintf("the global %q is already here: its value was kept", kv.Key))
				continue
			}
			a.ws.Globals = append(a.ws.Globals, kv)
			added++
		}
		parts = append(parts, count(added, "global"))
		if err == nil {
			err = a.ws.SaveGlobals()
		}
	}
	rep.title = file + ": " + strings.Join(parts, "; ")
	if err != nil {
		rep.notes = append(rep.notes, "not all of it was saved: "+err.Error())
	}
	for _, w := range res.Warnings {
		rep.notes = append(rep.notes, w.String())
	}
	return rep
}

// freeName returns name, or name with the first number from 2 that makes
// it one not taken.
func freeName(name string, taken func(string) bool) string {
	out := name
	for n := 2; taken(out); n++ {
		out = name + " " + strconv.Itoa(n)
	}
	return out
}

// count writes a number of things: "1 request", "3 requests".
func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

func countRequests(f *model.Folder) int {
	n := len(f.Requests)
	for _, sub := range f.Folders {
		n += countRequests(sub)
	}
	return n
}

// importDialog shows what the last import did.
func (a *App) importDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.reporting, func() {
		ui.Column(c).Width(560).Height(420).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "Imported").Bold()
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(t.Space(1)).Children(func() {
					for i, rep := range a.imported {
						ui.Text(c.Key("file:"+strconv.Itoa(i)), rep.title)
						for j, note := range rep.notes {
							ui.Text(c.Key(fmt.Sprintf("note:%d:%d", i, j)), "• "+note).TextColor(t.TextMuted)
						}
					}
				})
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "Done").Clicked() {
					a.reporting = false
				}
			})
		})
	})
}

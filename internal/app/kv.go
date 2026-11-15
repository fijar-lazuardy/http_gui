package app

import (
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"

	"http_gui/internal/model"
)

// kvOptions say what a key/value editor shows besides keys and values.
type kvOptions struct {
	list   string // names the editor, unique in the window, for the focus
	secret bool   // a Secret check box per row, which masks the value
	// files gives each row a Text/File choice; chooseFile is called with
	// the ID of a File row whose file is to be chosen.
	files      bool
	chooseFile func(id string)
}

// kvField is a text input of a key/value editor, for the focus: of the row
// with this ID, the key (0) or the value (1).
type kvField struct {
	list, id string
	col      int
}

// kvEditor edits rows of keys and values, as parameters, headers, form
// fields and variables. An empty row is always last: typing in it adds a
// row. It reports whether the rows changed.
func (a *App) kvEditor(c *ui.Context, rows *[]model.KV, opt kvOptions) (changed bool) {
	t := c.Theme()
	removed := ""
	ui.Column(c.Key(opt.list)).FillWidth().Gap(t.Space(1)).Children(func() {
		for i := range *rows {
			row := &(*rows)[i]
			before := *row
			ui.Row(c.Key(row.ID)).FillWidth().Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &row.Enabled, "").Label("Enabled")
				ui.TextInput(c, &row.Key).Placeholder("Key").Label("Key").Basis(0).Grow(1).
					FocusBind(&a.kvFocus, kvField{opt.list, row.ID, 0})
				if opt.files {
					kind := "Text"
					if row.File {
						kind = "File"
					}
					ui.Select(c, &kind, []string{"Text", "File"}).Label("Kind").Width(84)
					if file := kind == "File"; file != row.File {
						row.File, row.Value = file, ""
					}
				}
				if row.File {
					name := filepath.Base(row.Value)
					if row.Value == "" {
						name = "No file"
					}
					ui.Text(c, name).SingleLine().Tooltip(row.Value).Basis(0).Grow(2)
					if ui.Button(c, "Choose…").Clicked() && opt.chooseFile != nil {
						opt.chooseFile(row.ID)
					}
				} else {
					value := ui.TextInput(c, &row.Value).Placeholder("Value").Label("Value").Basis(0).Grow(2).
						FocusBind(&a.kvFocus, kvField{opt.list, row.ID, 1})
					if row.Secret {
						value.Password()
					}
				}
				if opt.secret {
					ui.Checkbox(c, &row.Secret, "Secret")
				}
				if ui.Button(c, "×").Label("Delete row").Tooltip("Delete").Width(t.Space(8)).Clicked() {
					removed = row.ID
				}
			})
			if *row != before {
				changed = true
			}
		}

		// The empty row: what is typed in it becomes a row, and the focus
		// moves to that row's input.
		var draft model.KV
		ui.Row(c.Key("")).FillWidth().Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, ptr(false), "").Label("Enabled").Disabled(true)
			ui.TextInput(c, &draft.Key).Placeholder("Key").Label("New key").Basis(0).Grow(1).
				FocusBind(&a.kvFocus, kvField{opt.list, "", 0})
			if opt.files {
				ui.Box(c).Width(84)
			}
			ui.TextInput(c, &draft.Value).Placeholder("Value").Label("New value").Basis(0).Grow(2).
				FocusBind(&a.kvFocus, kvField{opt.list, "", 1})
			if opt.secret {
				ui.Checkbox(c, ptr(false), "Secret").Disabled(true)
			}
			ui.Box(c).Width(t.Space(8))
		})
		if draft.Key != "" || draft.Value != "" {
			draft.ID, draft.Enabled = model.NewID(), true
			*rows = append(*rows, draft)
			col := 0
			if draft.Key == "" {
				col = 1
			}
			a.kvFocus = kvField{opt.list, draft.ID, col}
			changed = true
		}
	})
	if removed != "" {
		*rows = slices.DeleteFunc(*rows, func(kv model.KV) bool { return kv.ID == removed })
		changed = true
	}
	return changed
}

// environments is the dialog editing the environments and the globals.
func (a *App) environments(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.envDialog, func() {
		ui.Row(c).Width(720).Height(440).Gap(t.Space(4)).Children(func() {
			ui.Column(c).Width(180).FillHeight().Gap(t.Space(1)).Children(func() {
				ui.Text(c, "Variables").Bold()
				a.envChoice(c, "", "Globals")
				for _, e := range a.ws.Environments {
					a.envChoice(c, e.ID, e.Name)
				}
				ui.Spacer(c)
				if ui.Button(c, "+ New environment").Clicked() {
					e := &model.Environment{Name: "New environment"}
					if err := a.ws.SaveEnvironment(e); err != nil {
						a.fail(err)
					} else {
						a.ws.Environments = append(a.ws.Environments, e)
						a.envEdit = e.ID
					}
				}
			})
			ui.Column(c.Key(a.envEdit)).Grow(1).FillHeight().Gap(t.Space(2)).Children(func() {
				e := a.findEnv(a.envEdit)
				if e == nil {
					ui.Text(c, "Globals").Bold()
					ui.Text(c, "Variables every request sees, under its environment's and collection's.").TextColor(t.TextMuted)
					ui.Scroll(c).Grow(1).FillWidth().Children(func() {
						if a.kvEditor(c, &a.ws.Globals, kvOptions{list: "globals", secret: true}) {
							a.changed("globals", a.ws.SaveGlobals)
						}
					})
					return
				}
				save := func() error { return a.ws.SaveEnvironment(e) }
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if ui.TextInput(c, &e.Name).Label("Environment name").Grow(1).Changed() {
						a.changed(e.ID, save)
					}
					if ui.Button(c, "Delete").Clicked() {
						a.flush()
						if err := a.ws.Delete(e.ID); err != nil {
							a.fail(err)
							return
						}
						a.ws.Environments = slices.DeleteFunc(a.ws.Environments, func(x *model.Environment) bool { return x == e })
						if a.envID == e.ID {
							a.envID = ""
						}
						a.envEdit = ""
					}
				})
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					if a.kvEditor(c, &e.Variables, kvOptions{list: "env", secret: true}) {
						a.changed(e.ID, save)
					}
				})
			})
		})
	})
}

func (a *App) envChoice(c *ui.Context, id, name string) {
	t := c.Theme()
	b := ui.Button(c.Key("env:"+id), name).FillWidth()
	if a.envEdit == id {
		b.Background(t.Surface).Border(1, t.Accent)
	}
	if b.Clicked() {
		a.flush()
		a.envEdit = id
	}
}

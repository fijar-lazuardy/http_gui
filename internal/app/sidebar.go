package app

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"http_gui/internal/model"
)

const noEnvironment = "No environment"

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(t.Space(2)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			a.environmentSelect(c)
			if ui.Button(c, "Edit").Tooltip("Environments and globals").Clicked() {
				a.envDialog = true
			}
		})
		if ui.Button(c, "+ New collection").Clicked() {
			a.newCollection()
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if len(a.ws.Collections) == 0 {
				ui.Text(c, "No collections yet.").TextColor(t.TextMuted)
				return
			}
			tree := ui.TreeBase(c)
			tree.Root.FillWidth().Children(func() {
				for _, col := range a.ws.Collections {
					a.folderRow(c, tree, item{col: col, folder: &col.Folder})
				}
			})
		})
	})
	if later := a.later; later != nil {
		a.later = nil
		later()
	}
}

func (a *App) environmentSelect(c *ui.Context) {
	options := []string{noEnvironment}
	chosen := noEnvironment
	for _, e := range a.ws.Environments {
		options = append(options, e.Name)
		if e.ID == a.envID {
			chosen = e.Name
		}
	}
	before := chosen
	ui.Select(c, &chosen, options).Label("Environment").Grow(1)
	if chosen != before {
		a.envID = ""
		if i := slices.Index(options, chosen); i > 0 {
			a.envID = a.ws.Environments[i-1].ID
		}
	}
}

// folderRow builds the row of a collection or a folder, and the rows inside it.
func (a *App) folderRow(c *ui.Context, tree ui.TreeParts, it item) {
	f := it.folder
	open, ok := a.tree[f.ID]
	if !ok {
		open = new(bool)
		a.tree[f.ID] = open
	}
	c.Key(f.ID)
	parts := tree.Item(open)
	a.row(c, parts, it, func() {
		ui.Text(c, chevron(*open)).TextColor(c.Theme().TextMuted).Width(12)
		label := ui.Text(c, f.Name).SingleLine().Grow(1)
		if it.parent == nil {
			label.Bold()
		}
	}, func() { *open = !*open })
	parts.Group(func() {
		for _, sub := range f.Folders {
			a.folderRow(c, tree, item{col: it.col, parent: f, folder: sub})
		}
		for _, r := range f.Requests {
			a.requestRow(c, tree, item{col: it.col, parent: f, req: r})
		}
	})
}

func (a *App) requestRow(c *ui.Context, tree ui.TreeParts, it item) {
	r := it.req
	c.Key(r.ID)
	parts := tree.Item(nil)
	a.row(c, parts, it, func() {
		color := methodColor(c.Theme(), r.Method)
		if r == a.open {
			color = c.Theme().AccentText // on the accent of the row chosen
		}
		ui.Text(c, methodLabel(r.Method)).FontSize(10).Bold().Width(44).TextColor(color)
		ui.Text(c, r.Name).SingleLine().Grow(1)
	}, func() { a.openRequest(r) })
}

// row styles a tree row as the theme's tree does, and gives it the menu of
// the items.
func (a *App) row(c *ui.Context, parts ui.TreeItemParts, it item, content, clicked func()) {
	t := c.Theme()
	row := parts.Row.Height(t.Space(7)).Gap(t.Space(1)).AlignItems(ui.Center).
		Padding(0, t.Space(2)).PaddingStart(t.Space(1 + 4*float32(parts.Depth()))).Radius(t.Radius)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Selected(it.req != nil && it.req == a.open)
	if row.Clicked() {
		clicked()
	}
	row.ContextMenu(func(m *ui.Menu) { a.menu(m, it) })
	row.Children(content)
}

// menu builds the menu of a row. It runs as the tree builds, so what it
// adds to the tree waits in a.later until the tree is built.
func (a *App) menu(m *ui.Menu, it item) {
	if it.req == nil {
		if m.Item("New request").Chosen() {
			a.later = func() { a.newRequest(it.folder) }
		}
		if m.Item("New folder").Chosen() {
			a.later = func() { a.newFolder(it.folder) }
		}
		m.Separator()
	}
	if m.Item("Rename").Chosen() {
		a.target, a.newName, a.renaming = it, it.name(), true
	}
	if m.Item("Duplicate").Chosen() {
		a.later = func() { a.duplicate(it) }
	}
	m.Separator()
	if m.Item("Delete").Chosen() {
		a.target, a.deleting = it, true
	}
}

func (a *App) dialogs(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.renaming, func() {
		ui.Column(c).Width(320).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "Rename “"+a.target.name()+"”").Bold()
			done := ui.TextInput(c, &a.newName).Label("Name").AutoFocus().Submitted()
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.renaming = false
				}
				if ui.PrimaryButton(c, "Rename").Clicked() || done {
					a.rename(a.target, a.newName)
					a.renaming = false
				}
			})
		})
	})
	if a.deleting {
		what := "“" + a.target.name() + "”"
		msg := "Its file is deleted. You can't undo this."
		if a.target.req == nil {
			msg = "It is deleted with everything in it. You can't undo this."
		}
		if ui.AlertDialog(c, &a.deleting, "Delete "+what+"?", msg, "Cancel", "Delete") == 1 {
			a.delete(a.target)
		}
	}
	a.environments(c)
}

func (a *App) openRequest(r *model.Request) {
	if a.open == r {
		return
	}
	a.stop()
	a.open = r
	a.result = nil
	a.loadEditors()
}

func (a *App) newCollection() {
	col := &model.Collection{Folder: model.Folder{Name: "New collection", Seq: len(a.ws.Collections)}}
	if err := a.ws.SaveCollection(col); err != nil {
		a.fail(err)
		return
	}
	a.ws.Collections = append(a.ws.Collections, col)
}

func (a *App) newRequest(parent *model.Folder) {
	r := &model.Request{Name: "New request", Method: "GET", Seq: nextSeq(parent), Body: model.Body{Kind: model.BodyNone}}
	if err := a.ws.SaveRequest(parent, r); err != nil {
		a.fail(err)
		return
	}
	parent.Requests = append(parent.Requests, r)
	a.showInside(parent)
	a.openRequest(r)
}

func (a *App) newFolder(parent *model.Folder) {
	f := &model.Folder{Name: "New folder", Seq: nextSeq(parent)}
	if err := a.ws.SaveFolder(parent, f); err != nil {
		a.fail(err)
		return
	}
	parent.Folders = append(parent.Folders, f)
	a.showInside(parent)
}

// showInside opens a folder, to show what was just added to it.
func (a *App) showInside(f *model.Folder) {
	if open, ok := a.tree[f.ID]; ok {
		*open = true
	} else {
		a.tree[f.ID] = ptr(true)
	}
}

func (a *App) rename(it item, name string) {
	if name == "" || name == it.name() {
		return
	}
	a.flush()
	var err error
	switch {
	case it.req != nil:
		it.req.Name = name
		err = a.ws.SaveRequest(it.parent, it.req)
	case it.parent == nil:
		it.col.Name = name
		err = a.ws.SaveCollection(it.col)
	default:
		it.folder.Name = name
		err = a.ws.SaveFolder(it.parent, it.folder)
	}
	a.fail(err)
}

// duplicate saves a copy of an item beside it, with everything inside it.
func (a *App) duplicate(it item) {
	a.flush()
	switch {
	case it.req != nil:
		r := cloneRequest(it.req)
		r.ID, r.Name, r.Seq = "", r.Name+" copy", nextSeq(it.parent)
		if err := a.ws.SaveRequest(it.parent, r); err != nil {
			a.fail(err)
			return
		}
		it.parent.Requests = append(it.parent.Requests, r)
	case it.parent == nil:
		col := &model.Collection{Folder: *copyFolder(&it.col.Folder), Variables: slices.Clone(it.col.Variables)}
		col.Name, col.Seq = col.Name+" copy", len(a.ws.Collections)
		if err := a.ws.SaveCollection(col); err != nil {
			a.fail(err)
			return
		}
		a.ws.Collections = append(a.ws.Collections, col)
		a.fail(a.saveChildren(&col.Folder))
	default:
		f := copyFolder(it.folder)
		f.Name, f.Seq = f.Name+" copy", nextSeq(it.parent)
		if err := a.ws.SaveFolder(it.parent, f); err != nil {
			a.fail(err)
			return
		}
		it.parent.Folders = append(it.parent.Folders, f)
		a.fail(a.saveChildren(f))
	}
}

// saveChildren writes what a new folder holds.
func (a *App) saveChildren(f *model.Folder) error {
	for _, r := range f.Requests {
		if err := a.ws.SaveRequest(f, r); err != nil {
			return err
		}
	}
	for _, sub := range f.Folders {
		if err := a.ws.SaveFolder(f, sub); err != nil {
			return err
		}
		if err := a.saveChildren(sub); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) delete(it item) {
	a.flush()
	if err := a.ws.Delete(it.id()); err != nil {
		a.fail(err)
		return
	}
	switch {
	case it.req != nil:
		it.parent.Requests = slices.DeleteFunc(it.parent.Requests, func(r *model.Request) bool { return r == it.req })
	case it.parent == nil:
		a.ws.Collections = slices.DeleteFunc(a.ws.Collections, func(c *model.Collection) bool { return c == it.col })
	default:
		it.parent.Folders = slices.DeleteFunc(it.parent.Folders, func(f *model.Folder) bool { return f == it.folder })
	}
	if a.open != nil {
		if _, ok := a.find(a.open.ID); !ok {
			a.stop()
			a.open, a.result = nil, nil
		}
	}
}

// copyFolder returns a copy of f and everything in it, without IDs, so
// that saving it makes new ones.
func copyFolder(f *model.Folder) *model.Folder {
	out := &model.Folder{Name: f.Name, Seq: f.Seq}
	for _, r := range f.Requests {
		r = cloneRequest(r)
		r.ID = ""
		out.Requests = append(out.Requests, r)
	}
	for _, sub := range f.Folders {
		out.Folders = append(out.Folders, copyFolder(sub))
	}
	return out
}

// cloneRequest returns a copy of r that shares nothing with it.
func cloneRequest(r *model.Request) *model.Request {
	out := *r
	out.Params = slices.Clone(r.Params)
	out.Headers = slices.Clone(r.Headers)
	out.Body.Form = slices.Clone(r.Body.Form)
	return &out
}

func nextSeq(f *model.Folder) int {
	n := 0
	for _, r := range f.Requests {
		n = max(n, r.Seq+1)
	}
	for _, sub := range f.Folders {
		n = max(n, sub.Seq+1)
	}
	return n
}

func methodLabel(method string) string {
	switch method {
	case "DELETE":
		return "DEL"
	case "OPTIONS":
		return "OPT"
	case "":
		return "GET"
	}
	return method
}

func methodColor(t *ui.Theme, method string) ui.Color {
	switch method {
	case "GET", "":
		return t.Success
	case "POST":
		return t.Warning
	case "PUT", "PATCH":
		return t.Accent
	case "DELETE":
		return t.Danger
	}
	return t.TextMuted
}

func chevron(open bool) string {
	if open {
		return "▾"
	}
	return "▸"
}

func ptr[T any](v T) *T { return &v }

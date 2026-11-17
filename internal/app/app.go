// Package app is the window's state and its views: the sidebar of
// collections and environments, and the request and response panes.
// It is the only package besides main that imports mygo.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"http_gui/internal/model"
	"http_gui/internal/runner"
	"http_gui/internal/store"
)

// saveDelay is how long after the last edit the edited files are written.
const saveDelay = 500 * time.Millisecond

// App holds everything the window shows that lasts from frame to frame.
// The view is a method on it.
type App struct {
	ws      *store.Workspace
	envID   string         // chosen environment, "" for none
	open    *model.Request // the request being edited
	tree    map[string]*bool
	results map[string]*runner.Result // per-request responses, keyed by ID
	// update runs a function on the main thread and redraws: the window's
	// Update, or a test's own.
	update func(func())
	// chooseFile asks for a file to send in a multipart form.
	chooseFile func() ([]string, error)
	// chooseImport asks for the files to import, chooseFolder for the
	// folder of a Bruno collection.
	chooseImport func() ([]string, error)
	chooseFolder func() ([]string, error)

	sidebarWidth float32
	splitPercent float32 // of the request pane over the response pane

	// Saving: the files edited since the last write, by ID, and when they
	// are written. edits counts the edits, for what is computed from the
	// request.
	pending map[string]func() error
	edits   int
	seen    int
	saveAt  time.Time
	errs    []string // to show as toasts in the next frame

	// What a menu, a rename or a delete is about, and the menu's action
	// on the tree, run once the tree is built.
	later    func()
	target   item
	renaming bool
	newName  string
	deleting bool

	// What the last import did, and whether its dialog shows.
	imported  []importReport
	reporting bool

	// The environments dialog.
	envDialog bool
	envEdit   string // ID of the environment edited, "" for the globals

	kvFocus        kvField
	varPopup       bool   // variable suggestion popover is open
	varDismissText string // urlText when the popup was last dismissed

	// The open request's editors and tabs (step 06).
	urlText    string
	body       *editor.State
	pre, post  *editor.State
	versions   [3]uint64 // of body, pre and post, as last copied to the request
	reqTab     int
	scriptTab  int
	jsonErr    string
	missing    []string
	missingAt  [3]any // edits, envID and open that missing was computed for
	runtime    map[string]string
	run        int // counts runs, so that a late result of an old one is dropped
	cancel     context.CancelFunc
	sending    bool
	result     *runner.Result
	response   *editor.State
	raw        bool
	respTab    int
	respHeader ui.ListState
}

// item is a node of the tree: a collection, a folder or a request, with
// the folder holding it.
type item struct {
	col    *model.Collection
	parent *model.Folder  // nil for a collection
	folder *model.Folder  // the folder, or the collection's root
	req    *model.Request // nil unless a request
}

func (it item) id() string {
	if it.req != nil {
		return it.req.ID
	}
	return it.folder.ID
}

func (it item) name() string {
	if it.req != nil {
		return it.req.Name
	}
	return it.folder.Name
}

// New returns the state of a window showing ws. update is Window.Update,
// through which runs report back.
func New(ws *store.Workspace, update func(func())) *App {
	a := &App{
		ws:           ws,
		update:       update,
		tree:         map[string]*bool{},
		results:      map[string]*runner.Result{},
		pending:      map[string]func() error{},
		runtime:      map[string]string{},
		sidebarWidth: 260,
		splitPercent: 50,
		chooseFile: func() ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Choose a file to send"})
		},
		chooseImport: func() ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{
				Title:    "Import Postman or Bruno files",
				Filters:  []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
				Multiple: true,
			})
		},
		chooseFolder: func() ([]string, error) {
			return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Import a Bruno collection folder", Directory: true})
		},
	}
	for _, p := range ws.Problems {
		a.fail(fmt.Errorf("could not read %s: %w", p.Path, p.Err))
	}
	return a
}

// View builds the window.
func (a *App) View(c *ui.Context) {
	a.saveWhenDue(c)
	for _, msg := range a.errs {
		c.AddToast(ui.Toast{Title: msg, Type: "error", Timeout: 8 * time.Second})
	}
	a.errs = nil

	if a.open != nil {
		c.OnWindowShortcut(ui.Ctrl, ui.KeyEnter, func() {
			if !a.sending {
				a.send()
			}
		})
	}
	ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, func() { a.main(c) }).SplitReset(260).Fill()
	a.dialogs(c)
}

// fail shows an error in the next frame.
func (a *App) fail(err error) {
	if err != nil {
		a.errs = append(a.errs, err.Error())
	}
}

// changed notes that an item was edited: its file is written once the
// edits stop for saveDelay.
func (a *App) changed(id string, save func() error) {
	a.pending[id] = save
	a.edits++
}

// requestChanged notes that a request was edited.
func (a *App) requestChanged(r *model.Request) {
	a.changed(r.ID, func() error {
		it, ok := a.find(r.ID)
		if !ok {
			return nil // deleted since
		}
		return a.ws.SaveRequest(it.parent, r)
	})
}

// saveWhenDue writes the pending files once saveDelay has passed since the
// last edit, and otherwise asks for a frame then.
func (a *App) saveWhenDue(c *ui.Context) {
	if a.edits != a.seen {
		a.seen = a.edits
		a.saveAt = c.Now().Add(saveDelay)
	}
	if len(a.pending) == 0 {
		return
	}
	if wait := a.saveAt.Sub(c.Now()); wait > 0 {
		c.After(wait)
		return
	}
	a.flush()
}

// flush writes the pending files now, as before an item moves or goes.
func (a *App) flush() {
	for id, save := range a.pending {
		delete(a.pending, id)
		a.fail(save())
	}
}

// Flush writes what is still pending, as the window closes.
func (a *App) Flush() { a.flush() }

// find returns the item with this ID.
func (a *App) find(id string) (item, bool) {
	var walk func(col *model.Collection, f *model.Folder) (item, bool)
	walk = func(col *model.Collection, f *model.Folder) (item, bool) {
		for _, r := range f.Requests {
			if r.ID == id {
				return item{col: col, parent: f, req: r}, true
			}
		}
		for _, sub := range f.Folders {
			if sub.ID == id {
				return item{col: col, parent: f, folder: sub}, true
			}
			if it, ok := walk(col, sub); ok {
				return it, true
			}
		}
		return item{}, false
	}
	for _, col := range a.ws.Collections {
		if col.ID == id {
			return item{col: col, folder: &col.Folder}, true
		}
		if it, ok := walk(col, &col.Folder); ok {
			return it, true
		}
	}
	return item{}, false
}

// environment returns the chosen environment, nil for none.
func (a *App) environment() *model.Environment {
	return a.findEnv(a.envID)
}

func (a *App) findEnv(id string) *model.Environment {
	for _, e := range a.ws.Environments {
		if e.ID == id && id != "" {
			return e
		}
	}
	return nil
}

package main

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"http_gui/internal/app"
	"http_gui/internal/store"
)

// workspaceDir is where the workspace lives, in the user data directory.
func workspaceDir() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspace"), nil
}

func main() {
	mygo.App.WhenReady(func() {
		dir, err := workspaceDir()
		if err == nil {
			var ws *store.Workspace
			if ws, err = store.Open(dir); err == nil {
				open(ws)
				return
			}
		}
		mygo.Dialog.Error("HTTP GUI", "The workspace could not be opened: "+err.Error())
		mygo.App.Quit()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func open(ws *store.Workspace) {
	var win *mygo.Window
	a := app.New(ws, func(fn func()) { win.Update(fn) })
	win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "HTTP GUI",
		Width:     1100,
		Height:    720,
		MinWidth:  640,
		MinHeight: 400,
		// Opens where the user left it last time.
		StateKey: "main",
		Content:  ui.View(a.View),
	})
	// Edits are written a moment after they are made: write the last ones
	// before the window goes.
	win.OnClose(func(*mygo.CloseEvent) { a.Flush() })
}

package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame.
type app struct{}

func (a *app) view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		ui.Text(c, "HTTP GUI").FontSize(26).Bold()
		ui.Text(c, "Nothing to send yet.").TextColor(c.Theme().TextMuted)
	})
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "HTTP GUI",
			Width:     1100,
			Height:    720,
			MinWidth:  640,
			MinHeight: 400,
			// Opens where the user left it last time.
			StateKey: "main",
			Content:  ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

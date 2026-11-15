package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The view must build without a window: every later view test relies on it.
func TestView(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 1100, 720)
	if !tt.HasText("HTTP GUI") {
		t.Errorf("the placeholder does not show the app's name, texts %q", tt.Texts())
	}
}

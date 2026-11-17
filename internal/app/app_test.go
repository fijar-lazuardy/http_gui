package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"http_gui/internal/model"
	"http_gui/internal/store"
)

// fixture is a window on a workspace in a temporary folder:
//
//	My API
//	  Auth
//	    POST Login
//	  GET List users
//
// and an environment, Staging.
type fixture struct {
	t       *testing.T
	a       *App
	tt      *ui.Tester
	dir     string
	updates chan func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	ws, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	col := &model.Collection{Folder: model.Folder{Name: "My API"}}
	auth := &model.Folder{Name: "Auth"}
	login := &model.Request{Name: "Login", Method: "POST", Body: model.Body{Kind: model.BodyNone}}
	list := &model.Request{Name: "List users", Method: "GET", Seq: 1, Body: model.Body{Kind: model.BodyNone}}
	env := &model.Environment{Name: "Staging"}
	for _, err := range []error{
		ws.SaveCollection(col),
		ws.SaveFolder(&col.Folder, auth),
		ws.SaveRequest(auth, login),
		ws.SaveRequest(&col.Folder, list),
		ws.SaveEnvironment(env),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// What the window shows is what is on disk.
	if ws, err = store.Open(dir); err != nil {
		t.Fatal(err)
	}

	f := &fixture{t: t, dir: dir, updates: make(chan func(), 1)}
	f.a = New(ws, func(fn func()) { f.updates <- fn })
	f.tt = ui.NewTester(f.a.View, 1100, 720)
	return f
}

// click clicks the element showing s, failing the test if there is none.
func (f *fixture) click(s string) {
	f.t.Helper()
	if err := f.tt.Click(s); err != nil {
		f.t.Fatalf("click %q: %v; texts %q", s, err, f.tt.Texts())
	}
}

func (f *fixture) menu(row string, path ...string) {
	f.t.Helper()
	if err := f.tt.RightClick(row); err != nil {
		f.t.Fatalf("right-click %q: %v", row, err)
	}
	if err := f.tt.ChooseMenuItem(path...); err != nil {
		f.t.Fatalf("menu of %q, %q: %v; menu %q", row, path, err, f.tt.Menu())
	}
}

func (f *fixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.dir, rel))
	return err == nil
}

// The tree is how the user reaches a request: it must show what is on
// disk, and open the request clicked.
func TestTreeOpensRequest(t *testing.T) {
	f := newFixture(t)
	if !f.tt.HasText("My API") {
		t.Fatalf("the collection is not in the tree: %q", f.tt.Texts())
	}
	f.click("My API")
	f.click("Auth")
	if !f.tt.HasText("Login") || !f.tt.HasText("List users") {
		t.Fatalf("the opened folders do not show their requests: %q", f.tt.Texts())
	}
	f.click("List users")
	if f.a.open == nil || f.a.open.Name != "List users" {
		t.Fatalf("clicking a request opened %+v", f.a.open)
	}
}

// A request made from the menu is one the user can find again: in the
// tree, and on disk after a restart.
func TestNewRequestFromMenu(t *testing.T) {
	f := newFixture(t)
	f.menu("My API", "New request")
	if !f.tt.HasText("New request") {
		t.Fatalf("the new request is not in the tree: %q", f.tt.Texts())
	}
	if !f.exists("collections/my-api/new-request.json") {
		t.Error("the new request was not saved")
	}
	if f.a.open == nil || f.a.open.Name != "New request" {
		t.Errorf("the new request is not open: %+v", f.a.open)
	}
}

// Deleting removes files, so it asks first, and cancelling keeps them.
func TestDeleteAsksFirst(t *testing.T) {
	f := newFixture(t)
	f.click("My API")
	const file = "collections/my-api/list-users.json"

	f.menu("List users", "Delete")
	if !f.tt.HasText("Delete “List users”?") {
		t.Fatalf("no question before deleting: %q", f.tt.Texts())
	}
	f.click("Cancel")
	if !f.tt.HasText("List users") || !f.exists(file) {
		t.Fatal("cancelling the delete removed the request")
	}

	f.menu("List users", "Delete")
	f.click("Delete")
	if f.tt.HasText("List users") {
		t.Error("the deleted request is still in the tree")
	}
	if f.exists(file) {
		t.Error("the deleted request's file is still on disk")
	}
}

// Renaming moves the file, so that the folder stays readable in git.
func TestRename(t *testing.T) {
	f := newFixture(t)
	f.click("My API")
	f.menu("List users", "Rename")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type("All users")
	f.click("Rename")
	if !f.tt.HasText("All users") {
		t.Fatalf("the tree does not show the new name: %q", f.tt.Texts())
	}
	if !f.exists("collections/my-api/all-users.json") || f.exists("collections/my-api/list-users.json") {
		t.Error("the file was not moved to the new name")
	}
}

// Choosing an environment decides which variables a request is sent with.
func TestChooseEnvironment(t *testing.T) {
	f := newFixture(t)
	f.click(noEnvironment)
	f.click("Staging")
	if f.a.envID == "" || f.a.environment().Name != "Staging" {
		t.Fatalf("choosing Staging set envID %q", f.a.envID)
	}
}

// Edits reach the disk on their own, a moment after the user stops typing.
func TestEditsSave(t *testing.T) {
	f := newFixture(t)
	f.click("My API")
	f.click("List users")
	f.click("URL")
	f.tt.Type("example.com/users")
	f.tt.Advance(time.Second)
	data, err := os.ReadFile(filepath.Join(f.dir, "collections/my-api/list-users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"url": "example.com/users"`) {
		t.Errorf("the URL typed was not saved:\n%s", data)
	}
}

// Typing {{ in the URL shows variable names from the environment and
// collection, and picking one completes it.
func TestVarSuggestion(t *testing.T) {
	f := newFixture(t)
	env := f.a.ws.Environments[0]
	env.Variables = []model.KV{
		{ID: "1", Key: "base_url", Value: "https://api.example.com", Enabled: true},
		{ID: "2", Key: "api_key", Value: "secret", Enabled: true},
	}
	f.click("No environment")
	f.click("Staging")
	f.openNew()
	f.typeURL("{{")
	if !f.tt.HasText("{{base_url}}") {
		t.Fatalf("variable suggestion not shown: %q", f.tt.Texts())
	}
	if !f.tt.HasText("{{api_key}}") {
		t.Fatalf("api_key suggestion not shown: %q", f.tt.Texts())
	}
	f.click("{{base_url}}")
	if f.a.urlText != "{{base_url}}" {
		t.Errorf("clicking the suggestion set URL to %q, want {{base_url}}", f.a.urlText)
	}
	if f.tt.HasText("{{api_key}}") {
		t.Error("suggestions still visible after selection")
	}
}

// Typing more after {{ narrows the suggestions.
func TestVarSuggestionFilters(t *testing.T) {
	f := newFixture(t)
	env := f.a.ws.Environments[0]
	env.Variables = []model.KV{
		{ID: "1", Key: "base_url", Value: "https://api.example.com", Enabled: true},
		{ID: "2", Key: "api_key", Value: "secret", Enabled: true},
	}
	f.click("No environment")
	f.click("Staging")
	f.openNew()
	f.typeURL("{{api")
	if !f.tt.HasText("{{api_key}}") {
		t.Fatalf("api_key not suggested for '{{api': %q", f.tt.Texts())
	}
	if f.tt.HasText("{{base_url}}") {
		t.Error("base_url should not match 'api'")
	}
}

// Typing {{ in the body editor shows variable completions.
func TestBodyVarCompletion(t *testing.T) {
	f := newFixture(t)
	env := f.a.ws.Environments[0]
	env.Variables = []model.KV{
		{ID: "1", Key: "account_id", Value: "123", Enabled: true},
	}
	f.click("No environment")
	f.click("Staging")
	f.openNew()
	f.click("Body")
	f.click("JSON")
	f.click("JSON body")
	f.tt.Type("{{")
	f.tt.Frame()
	if !f.tt.HasText("account_id") {
		t.Fatalf("body editor did not show variable completion for '{{': %q", f.tt.Texts())
	}
}

// Typing {{ in a script editor shows variable completions.
func TestScriptVarCompletion(t *testing.T) {
	f := newFixture(t)
	env := f.a.ws.Environments[0]
	env.Variables = []model.KV{
		{ID: "1", Key: "token", Value: "abc", Enabled: true},
	}
	f.click("No environment")
	f.click("Staging")
	f.openNew()
	f.click("Scripts")
	f.click("Pre-request script")
	f.tt.Type("{{")
	f.tt.Frame()
	if !f.tt.HasText("token") {
		t.Fatalf("script editor did not show variable completion for '{{': %q", f.tt.Texts())
	}
}

// kvTester shows a key/value editor of rows alone.
func kvTester(rows *[]model.KV) (*App, *ui.Tester) {
	a := &App{}
	return a, ui.NewTester(func(c *ui.Context) { a.kvEditor(c, rows, kvOptions{list: "test"}) }, 700, 300)
}

// The empty row is how rows are added: what is typed goes on into the new
// row, not into another.
func TestKVTypingAddsRow(t *testing.T) {
	var rows []model.KV
	_, tt := kvTester(&rows)
	if err := tt.Click("New key"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Accept")
	if len(rows) != 1 || rows[0].Key != "Accept" || !rows[0].Enabled {
		t.Fatalf("typing in the empty row made %+v", rows)
	}
}

// An unchecked row stays in the list but is not sent.
func TestKVUncheck(t *testing.T) {
	rows := []model.KV{{ID: "1", Key: "a", Value: "1", Enabled: true}}
	_, tt := kvTester(&rows)
	if err := tt.Click("Enabled"); err != nil {
		t.Fatal(err)
	}
	if rows[0].Enabled {
		t.Error("unchecking left the row enabled")
	}
}

// Rows are told apart by ID, not key: headers may repeat a key.
func TestKVDeleteSharedKey(t *testing.T) {
	rows := []model.KV{
		{ID: "1", Key: "X-Tag", Value: "first", Enabled: true},
		{ID: "2", Key: "X-Tag", Value: "second", Enabled: true},
		{ID: "3", Key: "X-Tag", Value: "third", Enabled: true},
	}
	_, tt := kvTester(&rows)
	var deletes []ui.AccessInfo
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleButton && info.Name == "Delete row" {
			deletes = append(deletes, info)
		}
	}
	if len(deletes) != 3 {
		t.Fatalf("%d delete buttons for 3 rows", len(deletes))
	}
	b := deletes[1].Bounds
	tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	if len(rows) != 2 || rows[0].Value != "first" || rows[1].Value != "third" {
		t.Errorf("deleting the second row left %+v", rows)
	}
}

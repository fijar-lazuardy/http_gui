package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"http_gui/internal/model"
)

func kv(key, value string) model.KV {
	return model.KV{ID: model.NewID(), Key: key, Value: value, Enabled: true}
}

// mustSave fails the test on a save error.
func mustSave(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func reopen(t *testing.T, dir string) *Workspace {
	t.Helper()
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Problems) > 0 {
		t.Fatalf("problems reopening: %v", w.Problems)
	}
	return w
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The files are the user's data, and what they keep in git: whatever the
// model can hold must come back from disk as it was saved.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	w.Globals = []model.KV{kv("company", "acme")}
	env := &model.Environment{Name: "Staging", Variables: []model.KV{
		kv("url", "https://staging.example.com"),
		{ID: model.NewID(), Key: "token", Value: "s3cret", Enabled: true, Secret: true},
		{ID: model.NewID(), Key: "off", Value: "x"},
	}}

	login := &model.Request{
		Name: "Login", Seq: 1, Method: "POST", URL: "{{url}}/login",
		Headers:    []model.KV{kv("X-Trace", "1")},
		Body:       model.Body{Kind: model.BodyJSON, Raw: "{\n  \"user\": \"ada\"\n}"},
		PreScript:  model.Script{Lang: "ts", Source: "const a: number = 1;\n"},
		PostScript: model.Script{Lang: "js", Source: "pm.environment.set('token', 'x');"},
	}
	upload := &model.Request{
		Name: "Upload", Seq: 2, Method: "PUT", URL: "{{url}}/files",
		Body: model.Body{Kind: model.BodyFormData, Form: []model.KV{
			kv("title", "report"),
			{ID: model.NewID(), Key: "file", Value: "/tmp/report.pdf", Enabled: true, File: true},
		}},
	}
	search := &model.Request{
		Name: "Search", Seq: 1, Method: "POST", URL: "{{url}}/search?page=1",
		Params: []model.KV{kv("q", "go")},
		Body:   model.Body{Kind: model.BodyURLEncoded, Form: []model.KV{kv("scope", "all")}},
	}
	ping := &model.Request{Name: "Ping", Seq: 2, Method: "GET", URL: "{{url}}/ping", Body: model.Body{Kind: model.BodyNone}}

	tokens := &model.Folder{Name: "Tokens", Seq: 1, Requests: []*model.Request{upload}}
	auth := &model.Folder{Name: "Auth", Seq: 1, Folders: []*model.Folder{tokens}, Requests: []*model.Request{login}}
	col := &model.Collection{
		Folder:    model.Folder{Name: "My API", Folders: []*model.Folder{auth}, Requests: []*model.Request{search, ping}},
		Variables: []model.KV{kv("version", "v2")},
	}
	w.Collections = []*model.Collection{col}
	w.Environments = []*model.Environment{env}

	mustSave(t, w.SaveGlobals())
	mustSave(t, w.SaveEnvironment(env))
	mustSave(t, w.SaveCollection(col))
	mustSave(t, w.SaveFolder(&col.Folder, auth))
	mustSave(t, w.SaveFolder(auth, tokens))
	mustSave(t, w.SaveRequest(auth, login))
	mustSave(t, w.SaveRequest(tokens, upload))
	mustSave(t, w.SaveRequest(&col.Folder, search))
	mustSave(t, w.SaveRequest(&col.Folder, ping))

	got := reopen(t, dir)
	if !reflect.DeepEqual(got.Globals, w.Globals) {
		t.Errorf("globals\n got %+v\nwant %+v", got.Globals, w.Globals)
	}
	if !reflect.DeepEqual(got.Environments, w.Environments) {
		t.Errorf("environments\n got %+v\nwant %+v", got.Environments[0], env)
	}
	if !reflect.DeepEqual(got.Collections, w.Collections) {
		t.Errorf("collections differ after reopening\n got %+v\nwant %+v", got.Collections[0], col)
	}
}

// A rename moves the file, so the old name must not stay behind to come
// back as a second item the next time the workspace opens.
func TestRenameAndDeleteLeaveNoStaleFiles(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir)
	col := &model.Collection{Folder: model.Folder{Name: "API"}}
	auth := &model.Folder{Name: "Auth"}
	login := &model.Request{Name: "Login", Method: "POST"}
	mustSave(t, w.SaveCollection(col))
	mustSave(t, w.SaveFolder(&col.Folder, auth))
	mustSave(t, w.SaveRequest(auth, login))

	root := filepath.Join(dir, "collections", "api")
	if !exists(filepath.Join(root, "auth", "login.json")) {
		t.Fatal("the request is not where the layout says")
	}

	login.Name = "Sign in"
	mustSave(t, w.SaveRequest(auth, login))
	if exists(filepath.Join(root, "auth", "login.json")) || !exists(filepath.Join(root, "auth", "sign-in.json")) {
		t.Error("renaming a request did not move its file")
	}

	// Renaming a folder moves its directory, and what is in it is saved
	// in the new one afterwards.
	auth.Name = "Accounts"
	mustSave(t, w.SaveFolder(&col.Folder, auth))
	login.Method = "PUT"
	mustSave(t, w.SaveRequest(auth, login))
	if exists(filepath.Join(root, "auth")) || !exists(filepath.Join(root, "accounts", "sign-in.json")) {
		t.Error("renaming a folder did not move its directory")
	}

	got := reopen(t, dir)
	folders := got.Collections[0].Folders
	if len(folders) != 1 || len(folders[0].Requests) != 1 || folders[0].Requests[0].Method != "PUT" {
		t.Errorf("after the renames, folders %+v", folders)
	}

	mustSave(t, w.Delete(login.ID))
	if exists(filepath.Join(root, "accounts", "sign-in.json")) {
		t.Error("deleting a request left its file")
	}
	mustSave(t, w.Delete(auth.ID))
	if exists(filepath.Join(root, "accounts")) {
		t.Error("deleting a folder left its directory")
	}
	if err := w.SaveRequest(auth, login); err == nil {
		t.Error("saving into a deleted folder must fail, not recreate it")
	}
}

// Two names can make the same file name, and a request can be named after
// a file the store keeps for itself: neither may overwrite the other.
func TestSameFileNameDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir)
	col := &model.Collection{Folder: model.Folder{Name: "API", Seq: 7}}
	mustSave(t, w.SaveCollection(col))
	for _, name := range []string{"Get user?", "Get user!", "Collection", "Folder"} {
		mustSave(t, w.SaveRequest(&col.Folder, &model.Request{Name: name}))
	}
	// Saving again keeps each in its own file.
	a := &model.Request{Name: "List"}
	b := &model.Request{Name: "list"}
	for range 2 {
		mustSave(t, w.SaveRequest(&col.Folder, a))
		mustSave(t, w.SaveRequest(&col.Folder, b))
	}

	got := reopen(t, dir)
	if got.Collections[0].Seq != 7 {
		t.Error("a request named Collection overwrote collection.json")
	}
	names := map[string]int{}
	for _, r := range got.Collections[0].Requests {
		names[r.Name]++
	}
	want := map[string]int{"Get user?": 1, "Get user!": 1, "Collection": 1, "Folder": 1, "List": 1, "list": 1}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("requests %v, want %v", names, want)
	}
}

// One broken file, as a bad merge leaves, must not take the workspace with
// it, and must stay on disk for the user to repair.
func TestCorruptFileIsReportedAndKept(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir)
	col := &model.Collection{Folder: model.Folder{Name: "API"}}
	mustSave(t, w.SaveCollection(col))
	mustSave(t, w.SaveRequest(&col.Folder, &model.Request{Name: "Good"}))
	bad := filepath.Join(dir, "collections", "api", "bad.json")
	if err := os.WriteFile(bad, []byte("<<<<<<< HEAD"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Problems) != 1 || got.Problems[0].Path != bad {
		t.Errorf("problems %v, want one for %s", got.Problems, bad)
	}
	if rs := got.Collections[0].Requests; len(rs) != 1 || rs[0].Name != "Good" {
		t.Errorf("requests %+v, want the good one alone", rs)
	}
	if !exists(bad) {
		t.Error("the corrupt file was removed")
	}
}

// A file copied by hand carries the ID of the one it was copied from; the
// two must stay two items, or saving one would move the other's file.
func TestCopiedFileGetsItsOwnID(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir)
	col := &model.Collection{Folder: model.Folder{Name: "API"}}
	mustSave(t, w.SaveCollection(col))
	mustSave(t, w.SaveRequest(&col.Folder, &model.Request{Name: "One"}))
	data, _ := os.ReadFile(filepath.Join(dir, "collections", "api", "one.json"))
	if err := os.WriteFile(filepath.Join(dir, "collections", "api", "two.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	rs := reopen(t, dir).Collections[0].Requests
	if len(rs) != 2 || rs[0].ID == rs[1].ID {
		t.Errorf("requests %+v, want two with IDs of their own", rs)
	}
}

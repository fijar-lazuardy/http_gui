package app

import (
	"os"
	"path/filepath"
	"testing"

	"http_gui/internal/store"
)

const shopCollection = `{
  "info": {"name": "Shop", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [{
    "name": "Orders",
    "item": [{
      "name": "Create order",
      "request": {
        "auth": {"type": "oauth2", "oauth2": []},
        "method": "POST",
        "header": [],
        "body": {"mode": "raw", "raw": "{\"item\": 1}", "options": {"raw": {"language": "json"}}},
        "url": {"raw": "{{baseUrl}}/orders"}
      }
    }]
  }]
}`

// importing writes a file and imports it through the Import menu.
func (f *fixture) importing(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.a.chooseImport = func() ([]string, error) { return []string{path}, nil }
	f.chooseImport(importFiles)
}

// chooseImport chooses an item of the Import menu and waits for the
// import to report back.
func (f *fixture) chooseImport(item string) {
	f.t.Helper()
	f.click("Import")
	if err := f.tt.ChooseMenuItem(item); err != nil {
		f.t.Fatalf("Import menu, %q: %v; menu %q", item, err, f.tt.Menu())
	}
	(<-f.updates)()
	f.tt.Frame()
}

// Importing is how a Postman user's work gets here: the collection must
// show in the tree, open, and be on disk for the next start; and what was
// not kept must be said.
func TestImportCollection(t *testing.T) {
	f := newFixture(t)
	f.importing("shop.postman_collection.json", shopCollection)

	for _, want := range []string{
		"shop.postman_collection.json: collection “Shop”, 1 request",
		`Orders/Create order: auth of type "oauth2" is not supported`,
	} {
		if !f.tt.HasText(want) {
			t.Errorf("the report lacks %q; texts %q", want, f.tt.Texts())
		}
	}
	f.click("Done")

	f.click("Shop")
	f.click("Orders")
	f.click("Create order")
	if f.a.open == nil || f.a.open.URL != "{{baseUrl}}/orders" || f.a.open.Body.Raw != `{"item": 1}` {
		t.Fatalf("opened %+v", f.a.open)
	}

	ws, err := store.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Problems) != 0 || len(ws.Collections) != 2 {
		t.Fatalf("on disk: %d collections, problems %v", len(ws.Collections), ws.Problems)
	}
	shop := ws.Collections[1]
	if shop.Name != "Shop" || len(shop.Folders) != 1 || len(shop.Folders[0].Requests) != 1 {
		t.Errorf("on disk: %+v", shop)
	}
}

// Importing a file twice must not write over the first import, which the
// user may have edited since.
func TestImportKeepsWhatIsThere(t *testing.T) {
	f := newFixture(t)
	f.importing("shop.json", shopCollection)
	f.click("Done")
	f.importing("shop.json", shopCollection)
	if !f.tt.HasText("collection “Shop 2”") {
		t.Errorf("texts %q", f.tt.Texts())
	}
	if !f.exists("collections/shop/collection.json") || !f.exists("collections/shop-2/collection.json") {
		t.Error("the two imports are not both on disk")
	}

	// Staging is the fixture's environment.
	f.click("Done")
	f.importing("staging.json", `{"name": "Staging", "values": [{"key": "baseUrl", "value": "https://staging.example"}]}`)
	if !f.tt.HasText("environment “Staging 2”, 1 variable") {
		t.Errorf("texts %q", f.tt.Texts())
	}
	if n := len(f.a.ws.Environments); n != 2 {
		t.Errorf("%d environments", n)
	}
}

// Globals join the workspace's, and one that is already there keeps the
// value the user gave it.
func TestImportGlobals(t *testing.T) {
	f := newFixture(t)
	f.importing("globals.json", `{"_postman_variable_scope": "globals", "values": [{"key": "tenant", "value": "acme"}]}`)
	f.click("Done")
	f.a.ws.Globals[0].Value = "mine"
	f.importing("globals.json", `{"_postman_variable_scope": "globals", "values": [
		{"key": "tenant", "value": "acme"}, {"key": "region", "value": "eu"}]}`)
	if !f.tt.HasText("globals.json: 1 global") || !f.tt.HasText(`the global "tenant" is already here`) {
		t.Errorf("texts %q", f.tt.Texts())
	}

	ws, err := store.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Globals) != 2 || ws.Globals[0].Value != "mine" || ws.Globals[1].Key != "region" {
		t.Errorf("globals on disk: %+v", ws.Globals)
	}
}

// A file that is not Postman's must say so and leave the workspace alone.
func TestImportRefusesOtherFiles(t *testing.T) {
	f := newFixture(t)
	f.importing("package.json", `{"name": "package", "version": "1.0.0"}`)
	if !f.tt.HasText("package.json was not imported") || !f.tt.HasText("not a Postman collection or environment") {
		t.Errorf("texts %q", f.tt.Texts())
	}
	if len(f.a.ws.Collections) != 1 || len(f.a.ws.Environments) != 1 {
		t.Error("the workspace changed")
	}
}

// importingFolder writes the files of a folder and imports it as a Bruno
// collection.
func (f *fixture) importingFolder(files map[string]string) {
	f.t.Helper()
	dir := filepath.Join(f.t.TempDir(), "pets")
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	f.a.chooseFolder = func() ([]string, error) { return []string{dir}, nil }
	f.chooseImport(importFolder)
}

// A Bruno user's work is a folder: choosing it must bring its requests
// and its environments, and keep them for the next start.
func TestImportBrunoFolder(t *testing.T) {
	f := newFixture(t)
	f.importingFolder(map[string]string{
		"bruno.json":             `{"version": "1", "name": "Pets", "type": "collection"}`,
		"Get pet.bru":            "meta {\n  name: Get pet\n  type: http\n  seq: 1\n}\n\nget {\n  url: {{baseUrl}}/pets/1\n  body: none\n  auth: oauth2\n}\n",
		"environments/Local.bru": "vars {\n  baseUrl: http://localhost:8080\n}\n",
	})
	for _, want := range []string{
		"pets: collection “Pets”, 1 request; environment “Local”, 1 variable",
		`Get pet: auth of type "oauth2" is not supported`,
	} {
		if !f.tt.HasText(want) {
			t.Errorf("the report lacks %q; texts %q", want, f.tt.Texts())
		}
	}
	f.click("Done")

	f.click("Pets")
	f.click("Get pet")
	if f.a.open == nil || f.a.open.URL != "{{baseUrl}}/pets/1" {
		t.Fatalf("opened %+v", f.a.open)
	}
	// The environment can be chosen, as one made here.
	f.click(noEnvironment)
	f.click("Local")
	if env := f.a.environment(); env == nil || env.Name != "Local" {
		t.Errorf("chose %+v", env)
	}

	ws, err := store.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Problems) != 0 || len(ws.Collections) != 2 || len(ws.Environments) != 2 {
		t.Fatalf("on disk: %d collections, %d environments, problems %v", len(ws.Collections), len(ws.Environments), ws.Problems)
	}
	if pets := ws.Collections[1]; pets.Name != "Pets" || len(pets.Requests) != 1 {
		t.Errorf("on disk: %+v", pets)
	}
}

// The file Bruno exports goes through the same menu item as Postman's:
// the user should not have to know which tool wrote a JSON file.
func TestImportBrunoExport(t *testing.T) {
	f := newFixture(t)
	f.importing("pets.json", `{"name": "Pets", "version": "1", "items": [
		{"type": "http-request", "name": "Get pet", "seq": 1,
		 "request": {"url": "{{baseUrl}}/pets/1", "method": "GET", "headers": [], "params": [], "body": {"mode": "none"}}}]}`)
	if !f.tt.HasText("pets.json: collection “Pets”, 1 request") {
		t.Errorf("texts %q", f.tt.Texts())
	}
}

// A folder that is not a Bruno collection must say so and leave the
// workspace alone.
func TestImportRefusesOtherFolders(t *testing.T) {
	f := newFixture(t)
	f.importingFolder(map[string]string{"README.md": "# not a collection\n"})
	if !f.tt.HasText("pets was not imported") || !f.tt.HasText("no bruno.json") {
		t.Errorf("texts %q", f.tt.Texts())
	}
	if len(f.a.ws.Collections) != 1 {
		t.Error("the workspace changed")
	}
}

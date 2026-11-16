package bruno

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"http_gui/internal/importer"
)

// ImportDir reads a collection kept as a folder: bruno.json at its root, a
// .bru file for each request, folder.bru and collection.bru for what
// folders and the collection set, and environments/<name>.bru.
//
// Only bruno.json and .bru files are read: a collection's folder may also
// hold a .env file with secrets.
func ImportDir(dir string) (*importer.Result, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bruno.json"))
	if errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(dir, "opencollection.yml")); err == nil {
			return nil, errors.New("this Bruno collection is in the YAML format (opencollection.yml), which is not supported: only .bru collections are")
		}
		return nil, errors.New("not a Bruno collection: the folder has no bruno.json")
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("bruno.json: %w", err)
	}

	c := new(conv)
	col := &collection{Name: named(config.Name, filepath.Base(dir))}
	col.Root = c.readRoot(filepath.Join(dir, "collection.bru"), "collection.bru")
	col.Items = c.readItems(dir, "")
	col.Environments = c.readEnvironments(filepath.Join(dir, "environments"))
	return c.result(col), nil
}

// blocks reads a .bru file. rel is its path in the collection, for warnings.
func (c *conv) blocks(path, rel string) ([]block, bool) {
	data, err := os.ReadFile(path)
	if err == nil {
		var blocks []block
		if blocks, err = parse(string(data)); err == nil {
			return blocks, true
		}
	}
	c.warn(rel, "could not be read: %v", err)
	return nil, false
}

// readRoot reads folder.bru or collection.bru, nil if there is none.
func (c *conv) readRoot(path, rel string) *root {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	blocks, ok := c.blocks(path, rel)
	if !ok {
		return nil
	}
	f := c.file(rel, blocks)
	r := &root{Request: &f.request}
	r.Meta.Name, r.Meta.Seq = f.name, f.seq
	return r
}

func (c *conv) readItems(dir, rel string) []item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.warn(rel, "could not be read: %v", err)
		return nil
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		sub := name
		if rel != "" {
			sub = rel + "/" + name
		}
		switch {
		case e.IsDir():
			if strings.HasPrefix(name, ".") || name == "node_modules" || rel == "" && name == "environments" {
				continue
			}
			it := item{Type: "folder", Name: name}
			if it.Root = c.readRoot(filepath.Join(dir, name, "folder.bru"), sub+"/folder.bru"); it.Root != nil {
				it.Name, it.Seq = named(it.Root.Meta.Name, name), it.Root.Meta.Seq
			}
			it.Items = c.readItems(filepath.Join(dir, name), sub)
			// A folder of other files, as scripts, is not one of requests.
			if it.Root != nil || len(it.Items) > 0 {
				items = append(items, it)
			}
		case e.Type().IsRegular() && strings.HasSuffix(name, ".bru") && name != "folder.bru" && name != "collection.bru":
			blocks, ok := c.blocks(filepath.Join(dir, name), sub)
			if !ok {
				continue
			}
			f := c.file(sub, blocks)
			it := item{Type: f.kind + "-request", Name: named(f.name, strings.TrimSuffix(name, ".bru")), Seq: f.seq, Request: &f.request}
			if f.kind == "" {
				it.Type = "http-request"
			}
			items = append(items, it)
		}
	}
	return items
}

func (c *conv) readEnvironments(dir string) []environment {
	entries, _ := os.ReadDir(dir) // a collection need not have any
	var envs []environment
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".bru")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		rel := "environments/" + e.Name()
		blocks, ok := c.blocks(filepath.Join(dir, e.Name()), rel)
		if !ok {
			continue
		}
		env := environment{Name: name}
		for _, b := range blocks {
			switch b.name {
			case "vars":
				env.Variables = append(env.Variables, b.pairs...)
			case "vars:secret":
				// Names only: Bruno keeps the values elsewhere.
				for _, name := range b.list {
					p := pair{Enabled: true, Secret: true}
					if p.Name, ok = strings.CutPrefix(name, "~"); ok {
						p.Enabled = false
					}
					env.Variables = append(env.Variables, p)
				}
			case "color":
			default:
				c.warn(rel, "the block %q is not known: left out", b.name)
			}
		}
		envs = append(envs, env)
	}
	return envs
}

// bruFile is what a request's file, folder.bru or collection.bru says.
type bruFile struct {
	name    string // meta's
	kind    string // meta's type: "http", "graphql", "grpc", "ws"
	seq     float64
	request request
}

// A form field's value may end in @contentType(...), and is files when it
// is @file(path|path).
var contentType = regexp.MustCompile(`\s*@contentType\([^)]*\)\s*$`)

// file turns a file's blocks into the values the JSON export has.
func (c *conv) file(rel string, blocks []block) bruFile {
	var f bruFile
	r := &f.request
	for _, b := range blocks {
		get := func(name string) string {
			for _, p := range b.pairs {
				if p.Name == name {
					return p.Value
				}
			}
			return ""
		}
		typed := func(kind string) {
			for _, p := range b.pairs {
				p.Type = kind
				r.Params = append(r.Params, p)
			}
		}
		switch b.name {
		case "meta":
			f.name, f.kind = get("name"), get("type")
			f.seq, _ = strconv.ParseFloat(get("seq"), 64)
		case "get", "post", "put", "delete", "patch", "options", "head", "connect", "trace", "http":
			r.Method = b.name
			if b.name == "http" {
				r.Method = get("method")
			}
			r.URL, r.Auth.Mode = get("url"), get("auth")
			// The block names the body that is sent: a file may hold others.
			switch mode := get("body"); mode {
			case "form-urlencoded":
				r.Body.Mode = "formUrlEncoded"
			case "multipart-form":
				r.Body.Mode = "multipartForm"
			default:
				r.Body.Mode = mode
			}
		case "auth":
			r.Auth.Mode = get("mode")
		case "auth:bearer":
			r.Auth.Bearer.Token = get("token")
		case "auth:basic":
			r.Auth.Basic.Username, r.Auth.Basic.Password = get("username"), get("password")
		case "headers":
			r.Headers = b.pairs
		case "params:query", "query":
			typed("query")
		case "params:path":
			typed("path")
		case "body", "body:json":
			r.Body.JSON = b.text
		case "body:text":
			r.Body.Text = b.text
		case "body:xml":
			r.Body.XML = b.text
		case "body:sparql":
			r.Body.Sparql = b.text
		case "body:form-urlencoded":
			r.Body.FormURLEncoded = b.pairs
		case "body:multipart-form":
			for _, p := range b.pairs {
				p.Value = contentType.ReplaceAllString(p.Value, "")
				if list, ok := strings.CutPrefix(p.Value, "@file("); ok && strings.HasSuffix(list, ")") {
					p.Type, p.Value = "file", ""
					p.Files = strings.FieldsFunc(strings.TrimSuffix(list, ")"), func(r rune) bool { return r == '|' })
				}
				r.Body.MultipartForm = append(r.Body.MultipartForm, p)
			}
		case "vars:pre-request":
			r.Vars.Req = b.pairs
		case "vars:post-response":
			r.Vars.Res = b.pairs
		case "assert":
			r.Assertions = b.pairs
		case "script:pre-request":
			r.Script.Req = b.text
		case "script:post-response":
			r.Script.Res = b.text
		case "tests":
			r.Tests = b.text
		case "docs":
			r.Docs = b.text
		case "body:graphql", "body:graphql:vars", "body:file", "settings", "example":
			// Bodies the mapping reports by their mode, and what the app
			// has no place for: saved examples and Bruno's own settings.
		default:
			// Other kinds of auth are reported by the mode that names them.
			if !strings.HasPrefix(b.name, "auth:") {
				c.warn(rel, "the block %q is not known: left out", b.name)
			}
		}
	}
	return f
}

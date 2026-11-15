// Package store keeps a workspace on disk: JSON files in a folder, one per
// request, so that a workspace can live in git.
//
//	<workspace>/
//	  globals.json                         []model.KV
//	  environments/<name>.json             model.Environment
//	  collections/<collection>/
//	    collection.json                    name, variables
//	    <request>.json                     model.Request
//	    <folder>/folder.json               name, seq
//	    <folder>/<request>.json
//
// File names are slugs of the items' names, and IDs live inside the files.
// The store reads and writes files; the caller owns the tree in memory, and
// adds to and removes from it.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"http_gui/internal/model"
)

const (
	collectionFile = "collection.json"
	folderFile     = "folder.json"
)

// Problem is a file that could not be read. The file is left as it is.
type Problem struct {
	Path string
	Err  error
}

// Workspace is what a workspace folder holds.
type Workspace struct {
	Dir          string
	Globals      []model.KV
	Environments []*model.Environment
	Collections  []*model.Collection
	// Problems lists the files Open skipped.
	Problems []Problem

	// paths maps an item's ID to its file, or to its directory for a
	// folder or a collection.
	paths map[string]string
}

// Open reads the workspace in dir, which need not exist yet. Files that do
// not parse are skipped and listed in Problems.
func Open(dir string) (*Workspace, error) {
	w := &Workspace{Dir: filepath.Clean(dir), paths: map[string]string{}}

	if err := readJSON(filepath.Join(w.Dir, "globals.json"), &w.Globals); err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.problem(filepath.Join(w.Dir, "globals.json"), err)
	}

	envDir := filepath.Join(w.Dir, "environments")
	entries, err := readDir(envDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !isItemFile(e.Name()) {
			continue
		}
		path := filepath.Join(envDir, e.Name())
		env := &model.Environment{}
		if err := readJSON(path, env); err != nil {
			w.problem(path, err)
			continue
		}
		w.claim(&env.ID, path)
		w.Environments = append(w.Environments, env)
	}
	sort.SliceStable(w.Environments, func(i, j int) bool { return w.Environments[i].Name < w.Environments[j].Name })

	colDir := filepath.Join(w.Dir, "collections")
	entries, err = readDir(colDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(colDir, e.Name())
		col := &model.Collection{}
		if err := readJSON(filepath.Join(dir, collectionFile), col); err != nil {
			w.problem(filepath.Join(dir, collectionFile), err)
			continue
		}
		w.claim(&col.ID, dir)
		w.loadChildren(dir, &col.Folder)
		w.Collections = append(w.Collections, col)
	}
	sort.SliceStable(w.Collections, func(i, j int) bool {
		a, b := w.Collections[i], w.Collections[j]
		return a.Seq < b.Seq || a.Seq == b.Seq && a.Name < b.Name
	})
	return w, nil
}

// loadChildren reads the folders and requests in dir into f.
func (w *Workspace) loadChildren(dir string, f *model.Folder) {
	entries, err := readDir(dir)
	if err != nil {
		w.problem(dir, err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, "."):
		case e.IsDir():
			// A folder without a folder.json, or with one that does not
			// parse, is still shown, under its directory's name.
			child := &model.Folder{}
			if err := readJSON(filepath.Join(path, folderFile), child); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					w.problem(filepath.Join(path, folderFile), err)
				}
				*child = model.Folder{Name: name}
			}
			w.claim(&child.ID, path)
			w.loadChildren(path, child)
			f.Folders = append(f.Folders, child)
		case isItemFile(name):
			r := &model.Request{}
			if err := readJSON(path, r); err != nil {
				w.problem(path, err)
				continue
			}
			w.claim(&r.ID, path)
			f.Requests = append(f.Requests, r)
		}
	}
	sort.SliceStable(f.Folders, func(i, j int) bool {
		a, b := f.Folders[i], f.Folders[j]
		return a.Seq < b.Seq || a.Seq == b.Seq && a.Name < b.Name
	})
	sort.SliceStable(f.Requests, func(i, j int) bool {
		a, b := f.Requests[i], f.Requests[j]
		return a.Seq < b.Seq || a.Seq == b.Seq && a.Name < b.Name
	})
}

// SaveGlobals writes the global variables.
func (w *Workspace) SaveGlobals() error {
	return writeJSON(filepath.Join(w.Dir, "globals.json"), w.Globals)
}

// SaveEnvironment writes an environment, moving its file if it was renamed.
func (w *Workspace) SaveEnvironment(e *model.Environment) error {
	return w.saveFile(&e.ID, filepath.Join(w.Dir, "environments"), e.Name, e)
}

// SaveCollection writes a collection's name and variables, not its
// children, moving its directory if it was renamed.
func (w *Workspace) SaveCollection(c *model.Collection) error {
	return w.saveDir(&c.ID, filepath.Join(w.Dir, "collections"), c.Name, collectionFile, c)
}

// SaveFolder writes a folder's name and order, not its children, under
// parent, moving its directory if it was renamed. The parent, a folder or a
// collection's root, must have been saved.
func (w *Workspace) SaveFolder(parent, f *model.Folder) error {
	dir, err := w.dirOf(parent)
	if err != nil {
		return err
	}
	return w.saveDir(&f.ID, dir, f.Name, folderFile, f)
}

// SaveRequest writes a request under parent, moving its file if it was
// renamed. The parent must have been saved.
func (w *Workspace) SaveRequest(parent *model.Folder, r *model.Request) error {
	dir, err := w.dirOf(parent)
	if err != nil {
		return err
	}
	return w.saveFile(&r.ID, dir, r.Name, r)
}

// Delete removes the file of the request or environment with this ID, or
// the directory of the folder or collection, with everything in it.
func (w *Workspace) Delete(id string) error {
	path, ok := w.paths[id]
	if !ok {
		return fmt.Errorf("store: nothing saved with ID %q", id)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	for id, p := range w.paths {
		if p == path || within(p, path) {
			delete(w.paths, id)
		}
	}
	return nil
}

func (w *Workspace) dirOf(parent *model.Folder) (string, error) {
	dir, ok := w.paths[parent.ID]
	if !ok {
		return "", fmt.Errorf("store: the parent %q is not saved", parent.Name)
	}
	return dir, nil
}

func (w *Workspace) saveFile(id *string, dir, name string, v any) error {
	if *id == "" {
		*id = model.NewID()
	}
	old := w.paths[*id]
	path := w.pick(dir, name, ".json", old)
	if err := writeJSON(path, v); err != nil {
		return err
	}
	w.paths[*id] = path
	if old != "" && old != path {
		return os.Remove(old)
	}
	return nil
}

func (w *Workspace) saveDir(id *string, parent, name, meta string, v any) error {
	if *id == "" {
		*id = model.NewID()
	}
	old := w.paths[*id]
	dir := w.pick(parent, name, "", old)
	if old != "" && old != dir {
		if err := os.Rename(old, dir); err != nil {
			return err
		}
		for id, p := range w.paths {
			if within(p, old) {
				w.paths[id] = dir + strings.TrimPrefix(p, old)
			}
		}
	}
	w.paths[*id] = dir
	return writeJSON(filepath.Join(dir, meta), v)
}

// pick returns the path in dir for an item of this name: the slug of the
// name, with a number after it if another item has that path. old is the
// item's current path, which it keeps if it still fits.
func (w *Workspace) pick(dir, name, ext, old string) string {
	base := slug(name)
	for i := 1; ; i++ {
		n := base
		if i > 1 {
			n = fmt.Sprintf("%s-%d", base, i)
		}
		path := filepath.Join(dir, n+ext)
		if path == old {
			return path
		}
		if n+ext == collectionFile || n+ext == folderFile {
			continue
		}
		// Any error but "it exists" is left for the write to report.
		if _, err := os.Lstat(path); err != nil {
			return path
		}
	}
}

// claim records where the item with this ID is. An item without an ID, or
// with one already taken, as a file copied by hand, gets a new one.
func (w *Workspace) claim(id *string, path string) {
	if _, taken := w.paths[*id]; *id == "" || taken {
		*id = model.NewID()
	}
	w.paths[*id] = path
}

func (w *Workspace) problem(path string, err error) {
	w.Problems = append(w.Problems, Problem{Path: path, Err: err})
}

// within reports whether path is inside dir.
func within(path, dir string) bool {
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// isItemFile reports whether a file name is that of a request or an
// environment: JSON, and not a folder's own file or a temporary one.
func isItemFile(name string) bool {
	return strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".") &&
		name != collectionFile && name != folderFile
}

// slug turns a name into a file name: its letters and digits in lower
// case, with a hyphen for each run of anything else.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// readDir lists a directory, which may not exist.
func readDir(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeJSON writes v to path through a temporary file beside it, so that a
// crash leaves the old file or the new one, never half of one.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

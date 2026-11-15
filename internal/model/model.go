// Package model holds the values the app edits, sends and stores:
// collections of requests, and the environments their variables come from.
package model

import (
	"crypto/rand"
	"encoding/hex"
)

// KV is a row of a key/value list: a query parameter, a header, a form
// field or a variable.
type KV struct {
	ID      string `json:"id"` // stable, for UI keys
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Secret  bool   `json:"secret,omitempty"` // variables only: masked in the UI
	File    bool   `json:"file,omitempty"`   // multipart only: Value is a file path
}

// BodyKind says how a request's body is built.
type BodyKind string

const (
	BodyNone       BodyKind = "none"
	BodyJSON       BodyKind = "json"
	BodyFormData   BodyKind = "form-data"
	BodyURLEncoded BodyKind = "urlencoded"
)

// Body is a request's body. Raw is the text of a JSON body; Form is the
// fields of a multipart or URL-encoded one.
type Body struct {
	Kind BodyKind `json:"kind"`
	Raw  string   `json:"raw,omitempty"`
	Form []KV     `json:"form,omitempty"`
}

// Script is a pre-request or post-response script.
type Script struct {
	Lang   string `json:"lang"` // "js" or "ts"
	Source string `json:"source"`
}

// Request is one HTTP request as the user wrote it: URL, parameters,
// headers and body may hold {{variables}}.
type Request struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Seq        int    `json:"seq"` // order within its folder
	Method     string `json:"method"`
	URL        string `json:"url"`
	Params     []KV   `json:"params,omitempty"`
	Headers    []KV   `json:"headers,omitempty"`
	Body       Body   `json:"body"`
	PreScript  Script `json:"preScript,omitzero"`
	PostScript Script `json:"postScript,omitzero"`
}

// Folder groups requests and other folders. Its children are stored in
// files of their own, not in the folder's.
type Folder struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Seq      int        `json:"seq"`
	Folders  []*Folder  `json:"-"`
	Requests []*Request `json:"-"`
}

// Collection is a root folder with variables.
type Collection struct {
	Folder
	Variables []KV `json:"variables,omitempty"`
}

// Environment is a named set of variables, of which the user chooses one.
type Environment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Variables []KV   `json:"variables,omitempty"`
}

// NewID returns a random identifier for a new item.
func NewID() string {
	var b [8]byte
	rand.Read(b[:]) // never fails
	return hex.EncodeToString(b[:])
}

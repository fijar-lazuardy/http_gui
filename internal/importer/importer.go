// Package importer holds what the importers of other tools' files share:
// what an import gives, and how it says what it could not keep.
package importer

import (
	"net/url"
	"strings"

	"http_gui/internal/model"
)

// Warning is something in a file the app has no place for, or changed on
// the way in. Path names the item, as "Folder/Request"; it is empty for the
// collection itself.
type Warning struct {
	Path    string
	Message string
}

func (w Warning) String() string {
	if w.Path == "" {
		return w.Message
	}
	return w.Path + ": " + w.Message
}

// Result is what an import held. Items have no IDs yet: saving them gives
// them theirs.
type Result struct {
	Collection   *model.Collection
	Environments []*model.Environment
	Globals      []model.KV // of a globals file, nil otherwise
	Warnings     []Warning
}

// SplitQuery cuts the query off a URL and returns it as parameters, as the
// app keeps them: left in the URL it would be sent twice.
func SplitQuery(raw string) (base string, params []model.KV) {
	base, query, _ := strings.Cut(raw, "?")
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		params = append(params, model.KV{ID: model.NewID(), Key: unescape(k), Value: unescape(v), Enabled: true})
	}
	return base, params
}

// unescape undoes a query's encoding, which sending does again. Text that
// is not encoded, as a {{variable}} with a % in it, stays as it is.
func unescape(s string) string {
	if out, err := url.QueryUnescape(s); err == nil {
		return out
	}
	return s
}

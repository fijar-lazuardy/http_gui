// Package vars replaces {{name}} in a request with the values of
// variables, looked up in Postman's scopes and order.
package vars

import (
	crand "crypto/rand"
	"fmt"
	"maps"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"time"

	"http_gui/internal/model"
)

// maxDepth is how many times values holding {{names}} of their own are
// resolved again, which also ends variables that refer to each other.
const maxDepth = 10

var placeholder = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Scopes are the variables of one run. A name is looked up in Runtime,
// then Environment, then Collection, then Global.
type Scopes struct {
	Runtime, Environment, Collection, Global map[string]string
}

// Map returns the enabled rows as a scope. Of rows with the same key, the
// last wins.
func Map(rows []model.KV) map[string]string {
	m := make(map[string]string, len(rows))
	for _, kv := range rows {
		if kv.Enabled && kv.Key != "" {
			m[kv.Key] = kv.Value
		}
	}
	return m
}

// Apply writes a scope back to the rows it came from, after a run whose
// scripts may have changed it: a value that changed is set in its row, a
// new name gets a row, and the rows of a name that was unset are removed.
// Rows switched off are left alone. It returns the new rows, and whether
// they differ from the old.
func Apply(rows []model.KV, scope map[string]string) (out []model.KV, changed bool) {
	last := map[string]int{} // key -> its last enabled row, the one Map reads
	for i, kv := range rows {
		if kv.Enabled && kv.Key != "" {
			last[kv.Key] = i
		}
	}
	for i, kv := range rows {
		if kv.Enabled && kv.Key != "" {
			value, ok := scope[kv.Key]
			if !ok {
				changed = true
				continue
			}
			if i == last[kv.Key] && kv.Value != value {
				kv.Value, changed = value, true
			}
		}
		out = append(out, kv)
	}
	for _, key := range slices.Sorted(maps.Keys(scope)) {
		if _, ok := last[key]; !ok && key != "" {
			out = append(out, model.KV{ID: model.NewID(), Key: key, Value: scope[key], Enabled: true})
			changed = true
		}
	}
	return out, changed
}

// Lookup returns the value of a variable in the first scope that has it.
func (s *Scopes) Lookup(name string) (string, bool) {
	for _, scope := range []map[string]string{s.Runtime, s.Environment, s.Collection, s.Global} {
		if v, ok := scope[name]; ok {
			return v, true
		}
	}
	return "", false
}

// Resolve replaces each {{name}} in text with its value. A name without a
// value stays as it is written, never an empty string, and is returned in
// missing, as are names left by variables that refer to each other.
func (s *Scopes) Resolve(text string) (out string, missing []string) {
	out = text
	for range maxDepth {
		next := placeholder.ReplaceAllStringFunc(out, func(m string) string {
			name := placeholder.FindStringSubmatch(m)[1]
			if v, ok := dynamic(name); ok {
				return v
			}
			if v, ok := s.Lookup(name); ok {
				return v
			}
			return m
		})
		if next == out {
			break
		}
		out = next
	}
	for _, m := range placeholder.FindAllStringSubmatch(out, -1) {
		if !slices.Contains(missing, m[1]) {
			missing = append(missing, m[1])
		}
	}
	return out, missing
}

// ResolveRequest returns a copy of r with its variables resolved: in the
// URL, and in the keys and values of the enabled parameters, headers and
// form fields, and in the raw body. Scripts are left alone, and so is r.
func ResolveRequest(r *model.Request, s *Scopes) (resolved *model.Request, missing []string) {
	resolve := func(text string) string {
		out, names := s.Resolve(text)
		for _, name := range names {
			if !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
		}
		return out
	}
	rows := func(rows []model.KV) []model.KV {
		rows = slices.Clone(rows)
		for i := range rows {
			if rows[i].Enabled {
				rows[i].Key, rows[i].Value = resolve(rows[i].Key), resolve(rows[i].Value)
			}
		}
		return rows
	}

	out := *r
	out.URL = resolve(r.URL)
	out.Params = rows(r.Params)
	out.Headers = rows(r.Headers)
	out.Body.Raw = resolve(r.Body.Raw)
	out.Body.Form = rows(r.Body.Form)
	return &out, missing
}

// dynamic returns the value of one of Postman's dynamic variables, new for
// each place it is written.
func dynamic(name string) (string, bool) {
	switch name {
	case "$guid", "$randomUUID":
		return UUID(), true
	case "$timestamp":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	case "$isoTimestamp":
		return time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), true
	case "$randomInt":
		return strconv.Itoa(rand.IntN(1001)), true
	}
	return "", false
}

// UUID returns a random (version 4) UUID.
func UUID() string {
	var b [16]byte
	crand.Read(b[:]) // never fails
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

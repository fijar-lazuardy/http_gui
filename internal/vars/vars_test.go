package vars

import (
	"reflect"
	"regexp"
	"testing"

	"http_gui/internal/model"
)

func on(key, value string) model.KV { return model.KV{Key: key, Value: value, Enabled: true} }

// A name defined in several scopes takes the narrowest: what a script set
// for this run, then the environment chosen, then the collection, then the
// globals, as Postman does, so that imported collections behave the same.
func TestPrecedence(t *testing.T) {
	s := &Scopes{
		Runtime:     map[string]string{"v": "runtime"},
		Environment: map[string]string{"v": "environment"},
		Collection:  map[string]string{"v": "collection"},
		Global:      map[string]string{"v": "global"},
	}
	for _, want := range []string{"runtime", "environment", "collection", "global"} {
		if got, _ := s.Resolve("{{v}}"); got != want {
			t.Errorf("resolved to %q, want %q", got, want)
		}
		switch want {
		case "runtime":
			s.Runtime = nil
		case "environment":
			s.Environment = nil
		case "collection":
			s.Collection = nil
		}
	}
}

func TestResolve(t *testing.T) {
	s := &Scopes{Environment: map[string]string{
		"url": "https://google.com", "host": "{{url}}/api", "my var": "spaced", "empty": "",
	}}
	tests := []struct{ in, want string }{
		{`{"target": "{{url}}"}`, `{"target": "https://google.com"}`},
		{"{{ url }}/a/{{url}}", "https://google.com/a/https://google.com"},
		{"{{host}}/users", "https://google.com/api/users"}, // a value holding a variable
		{"{{my var}}", "spaced"},
		{"[{{empty}}]", "[]"}, // defined and empty is not missing
		{`{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"no variables", "no variables"},
	}
	for _, tt := range tests {
		got, missing := s.Resolve(tt.in)
		if got != tt.want || len(missing) != 0 {
			t.Errorf("Resolve(%q) = %q, missing %v, want %q", tt.in, got, missing, tt.want)
		}
	}
}

// A mistyped name must reach the server as written, where it is seen, and
// be reported: an empty string in its place would send a request that
// looks right and is not.
func TestUnknownNamesStayAndAreReported(t *testing.T) {
	s := &Scopes{Environment: map[string]string{"url": "https://x"}}
	got, missing := s.Resolve("{{url}}/{{nope}}/{{nope}}?k={{ other }}")
	if got != "https://x/{{nope}}/{{nope}}?k={{ other }}" {
		t.Errorf("resolved to %q", got)
	}
	if !reflect.DeepEqual(missing, []string{"nope", "other"}) {
		t.Errorf("missing %v, want each name once", missing)
	}
}

func TestCycleEnds(t *testing.T) {
	s := &Scopes{Global: map[string]string{"a": "{{b}}", "b": "{{a}}", "self": "x{{self}}"}}
	if _, missing := s.Resolve("{{a}}"); len(missing) != 1 {
		t.Errorf("missing %v, want the name the cycle stopped at", missing)
	}
	s.Resolve("{{self}}")
}

func TestDynamicVariables(t *testing.T) {
	s := &Scopes{}
	got, missing := s.Resolve("{{$guid}} {{$randomUUID}} {{$timestamp}} {{$isoTimestamp}} {{$randomInt}}")
	if len(missing) != 0 {
		t.Fatalf("missing %v", missing)
	}
	uuid := `[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`
	want := regexp.MustCompile(`^` + uuid + ` ` + uuid + ` \d{10} \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z \d{1,4}$`)
	if !want.MatchString(got) {
		t.Errorf("resolved to %q", got)
	}

	// Each place gets a value of its own: two IDs in one body differ.
	got, _ = s.Resolve("{{$guid}} {{$guid}}")
	if got[:36] == got[37:] {
		t.Errorf("{{$guid}} twice gave the same value %q", got[:36])
	}
	if got, missing := s.Resolve("{{$nope}}"); got != "{{$nope}}" || len(missing) != 1 {
		t.Errorf("an unknown dynamic variable resolved to %q, missing %v", got, missing)
	}
}

func TestMap(t *testing.T) {
	got := Map([]model.KV{on("a", "1"), {Key: "b", Value: "off"}, on("a", "2"), on("", "no key")})
	if want := map[string]string{"a": "2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Map = %v, want %v", got, want)
	}
}

// Every place of a request the user can type in takes variables, and the
// request they wrote keeps its {{names}} for the next run.
func TestResolveRequest(t *testing.T) {
	s := &Scopes{Environment: map[string]string{"v": "X", "name": "key"}}
	r := &model.Request{
		Method:  "POST",
		URL:     "http://{{v}}/p?{{name}}={{v}}",
		Params:  []model.KV{on("{{name}}", "{{v}}"), {Key: "off", Value: "{{unset}}"}},
		Headers: []model.KV{on("X-{{name}}", "Bearer {{v}}")},
		Body: model.Body{
			Kind: model.BodyJSON, Raw: `{"a": "{{v}}", "b": "{{missing}}"}`,
			Form: []model.KV{on("{{name}}", "{{v}}")},
		},
		PreScript: model.Script{Lang: "js", Source: "'{{v}}'"},
	}
	before := *r
	before.Params = append([]model.KV(nil), r.Params...)

	got, missing := ResolveRequest(r, s)
	want := &model.Request{
		Method:  "POST",
		URL:     "http://X/p?key=X",
		Params:  []model.KV{on("key", "X"), {Key: "off", Value: "{{unset}}"}},
		Headers: []model.KV{on("X-key", "Bearer X")},
		Body: model.Body{
			Kind: model.BodyJSON, Raw: `{"a": "X", "b": "{{missing}}"}`,
			Form: []model.KV{on("key", "X")},
		},
		PreScript: model.Script{Lang: "js", Source: "'{{v}}'"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved\n got %+v\nwant %+v", got, want)
	}
	// A row switched off is not sent, so its names are not missing.
	if !reflect.DeepEqual(missing, []string{"missing"}) {
		t.Errorf("missing %v", missing)
	}
	if r.URL != before.URL || !reflect.DeepEqual(r.Params, before.Params) || r.Body.Raw != before.Body.Raw {
		t.Error("resolving changed the request as the user wrote it")
	}
}

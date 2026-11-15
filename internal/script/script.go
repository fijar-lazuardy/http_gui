// Package script runs a request's pre-request and post-response scripts:
// JavaScript or TypeScript, with Postman's pm API, Bruno's bru, req and
// res over it, and crypto.
//
// A script comes from the user's files or from an imported collection, so
// it is not trusted: it runs in an interpreter (goja) with no access to
// files, the network, the environment or the clock's timers, and it is
// stopped at a deadline. Each run has an interpreter of its own.
package script

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/evanw/esbuild/pkg/api"

	"http_gui/internal/httpx"
	"http_gui/internal/model"
	"http_gui/internal/vars"
)

// DefaultTimeout is how long a script may run.
const DefaultTimeout = 10 * time.Second

var (
	//go:embed js/prelude.js
	preludeSource string
	//go:embed js/crypto-js.min.js
	cryptoJSSource string
	//go:embed js/chai.min.js
	chaiSource string

	// The embedded code is compiled once, and run in each interpreter
	// that uses it.
	preludeProgram  = sync.OnceValue(func() *goja.Program { return mustCompile("prelude.js", preludeSource) })
	cryptoJSProgram = sync.OnceValue(func() *goja.Program { return mustCompile("crypto-js.js", asModule(cryptoJSSource)) })
	chaiProgram     = sync.OnceValue(func() *goja.Program { return mustCompile("chai.js", asModule(chaiSource)) })
)

// LogLine is a line a script wrote with console.log and the like.
type LogLine struct {
	Level string // "log", "info", "warn", "error" or "debug"
	Text  string
}

// TestResult is the outcome of a pm.test.
type TestResult struct {
	Name   string
	Passed bool
	Error  string // why it failed
}

// Run is a script to run and what it sees.
type Run struct {
	Name     string          // "pre-request" or "post-response", for messages
	Script   model.Script    // Lang "ts" for TypeScript, anything else for JavaScript
	Request  *model.Request  // read by the script, never changed: see Output.Request
	Response *httpx.Response // nil before the request is sent
	Scopes   *vars.Scopes    // read and changed by the script
	Timeout  time.Duration   // 0 for DefaultTimeout
}

// Output is what a script did, complete up to where it stopped if it
// failed.
type Output struct {
	// Request is the request as a pre-request script left it. After a
	// post-response script, or a failure, it is Run.Request.
	Request *model.Request
	Console []LogLine
	Tests   []TestResult
}

// Exec runs a script to its end. The error is the script's own (a syntax
// error, an exception it threw, its deadline) or ctx's when ctx ends
// first.
func Exec(ctx context.Context, in Run) (out Output, err error) {
	out.Request = in.Request
	if in.Scopes == nil {
		in.Scopes = &vars.Scopes{}
	}
	if in.Timeout == 0 {
		in.Timeout = DefaultTimeout
	}

	file, loader := in.Name+".js", api.LoaderJS
	if in.Script.Lang == "ts" {
		file, loader = in.Name+".ts", api.LoaderTS
	}
	code, err := transform(file, loader, in.Script.Source)
	if err != nil {
		return out, err
	}
	// The source map esbuild wrote is inside the code; none is read from
	// a file, whatever a comment in the script names.
	ast, err := goja.Parse(file, code, parser.WithSourceMapLoader(func(string) ([]byte, error) {
		return nil, errors.New("source maps are not read from files")
	}))
	if err != nil {
		return out, err
	}
	program, err := goja.CompileAST(ast, false)
	if err != nil {
		return out, err
	}

	vm := goja.New()
	// require finds the modules below and nothing else: no file is read.
	registry := require.NewRegistry(require.WithLoader(func(string) ([]byte, error) {
		return nil, require.ModuleFileDoesNotExistError
	}))
	for _, name := range []string{"crypto", "node:crypto"} {
		registry.RegisterNativeModule(name, loadNodeCrypto)
	}
	registry.RegisterNativeModule("crypto-js", library(cryptoJSProgram))
	registry.RegisterNativeModule("chai", library(chaiProgram))
	registry.Enable(vm)
	buffer.Enable(vm)
	vm.Set("btoa", func(call goja.FunctionCall) goja.Value { return btoa(vm, call.Argument(0).String()) })
	vm.Set("atob", func(call goja.FunctionCall) goja.Value { return atob(vm, call.Argument(0).String()) })

	h := &host{vm: vm, in: in, out: &out}
	hostObject, err := h.object()
	if err != nil {
		return out, err
	}
	prelude, err := vm.RunProgram(preludeProgram())
	if err != nil {
		return out, fmt.Errorf("script API: %w", err)
	}
	start, _ := goja.AssertFunction(prelude)
	if _, err := start(goja.Undefined(), hostObject); err != nil {
		return out, fmt.Errorf("script API: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, in.Timeout)
	defer cancel()
	stop := context.AfterFunc(deadline, func() { vm.Interrupt(deadline.Err()) })
	defer stop()

	result, err := vm.RunProgram(program)
	if err == nil {
		err = settled(result)
	}
	var interrupted *goja.InterruptedError
	switch {
	case errors.As(err, &interrupted) && ctx.Err() != nil:
		return out, ctx.Err()
	case errors.As(err, &interrupted):
		return out, fmt.Errorf("the %s script ran for more than %v and was stopped", in.Name, in.Timeout)
	case err != nil:
		return out, err
	}

	// Only a pre-request script changes the request that is sent.
	if in.Response == nil {
		out.Request, err = h.request(hostObject)
	}
	return out, err
}

// wrapOpen goes before the script, on its first line so that the lines of
// errors are the script's: the script is the body of an async function,
// where await and return work.
const wrapOpen = "(async function () {"

// transform turns the script into JavaScript the interpreter runs: types
// removed, and syntax newer than ES2017 rewritten.
func transform(file string, loader api.Loader, source string) (string, error) {
	res := api.Transform(wrapOpen+source+"\n})()", api.TransformOptions{
		Loader:     loader,
		Target:     api.ES2017,
		Sourcefile: file,
		Sourcemap:  api.SourceMapInline,
	})
	if len(res.Errors) == 0 {
		return string(res.Code), nil
	}
	m := res.Errors[0]
	if m.Location == nil {
		return "", fmt.Errorf("%s: %s", file, m.Text)
	}
	column := m.Location.Column + 1
	if m.Location.Line == 1 {
		column -= len(wrapOpen)
	}
	return "", fmt.Errorf("%s:%d:%d: %s", file, m.Location.Line, column, m.Text)
}

// settled returns the error of the script's async function: what it threw,
// if it did.
func settled(result goja.Value) error {
	p, ok := result.Export().(*goja.Promise)
	if !ok {
		return nil
	}
	switch p.State() {
	case goja.PromiseStateRejected:
		reason := p.Result()
		if o, ok := reason.(*goja.Object); ok {
			if stack := o.Get("stack"); stack != nil && !goja.IsUndefined(stack) && stack.String() != "" {
				return errors.New(strings.TrimSpace(stack.String()))
			}
		}
		return errors.New(reason.String())
	case goja.PromiseStatePending:
		// Nothing a script can await finishes later: there are no timers
		// and no requests.
		return errors.New("the script awaits a promise that never settles")
	}
	return nil
}

func mustCompile(name, source string) *goja.Program {
	ast, err := goja.Parse(name, source, parser.WithDisableSourceMaps)
	if err != nil {
		panic(err)
	}
	program, err := goja.CompileAST(ast, false)
	if err != nil {
		panic(err)
	}
	return program
}

// asModule wraps a library's source as a function of a CommonJS module.
func asModule(source string) string {
	return "(function (module, exports) {" + source + "\n})"
}

// library is the loader of an embedded JavaScript library.
func library(program func() *goja.Program) require.ModuleLoader {
	return func(vm *goja.Runtime, module *goja.Object) {
		fn, err := vm.RunProgram(program())
		if err != nil {
			panic(vm.NewGoError(err))
		}
		load, _ := goja.AssertFunction(fn)
		if _, err := load(vm.GlobalObject(), module, module.Get("exports")); err != nil {
			panic(vm.NewGoError(err))
		}
	}
}

// host is what the script API in js/prelude.js is built on.
type host struct {
	vm  *goja.Runtime
	in  Run
	out *Output
}

func (h *host) object() (*goja.Object, error) {
	request, err := json.Marshal(h.in.Request)
	if err != nil {
		return nil, err
	}
	o := h.vm.NewObject()
	o.Set("request", string(request))
	o.Set("response", nil)
	if resp := h.in.Response; resp != nil {
		headers := [][2]string{}
		for _, name := range slices.Sorted(maps.Keys(resp.Headers)) {
			for _, value := range resp.Headers[name] {
				headers = append(headers, [2]string{name, value})
			}
		}
		meta, err := json.Marshal(map[string]any{
			"code":         resp.StatusCode,
			"status":       strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" "),
			"headers":      headers,
			"responseTime": resp.Duration.Milliseconds(),
			"size":         resp.Size,
		})
		if err != nil {
			return nil, err
		}
		o.Set("response", string(meta))
		o.Set("responseBody", string(resp.Body))
	}
	o.Set("getVar", h.getVar)
	o.Set("setVar", func(scope, name, value string) {
		m := h.scope(scope)
		if *m == nil {
			*m = map[string]string{}
		}
		(*m)[name] = value
	})
	o.Set("unsetVar", func(scope, name string) { delete(*h.scope(scope), name) })
	o.Set("vars", func(scope string) *goja.Object {
		all := h.vm.NewObject()
		for name, value := range *h.scope(scope) {
			all.Set(name, value)
		}
		return all
	})
	o.Set("replaceIn", func(text string) string {
		out, _ := h.in.Scopes.Resolve(text)
		return out
	})
	o.Set("url", h.url)
	o.Set("log", func(level, text string) {
		h.out.Console = append(h.out.Console, LogLine{Level: level, Text: text})
	})
	o.Set("test", func(name string, passed bool, message string) {
		h.out.Tests = append(h.out.Tests, TestResult{Name: name, Passed: passed, Error: message})
	})
	return o, nil
}

// scope returns the variables of a scope by its name in the prelude.
func (h *host) scope(name string) *map[string]string {
	s := h.in.Scopes
	switch name {
	case "environment":
		return &s.Environment
	case "collection":
		return &s.Collection
	case "global":
		return &s.Global
	}
	return &s.Runtime
}

// getVar returns a variable of a scope, or of the first scope that has it
// for the scope "", and undefined without one.
func (h *host) getVar(scope, name string) goja.Value {
	var value string
	var ok bool
	if scope == "" {
		value, ok = h.in.Scopes.Lookup(name)
	} else {
		value, ok = (*h.scope(scope))[name]
	}
	if !ok {
		return goja.Undefined()
	}
	return h.vm.ToValue(value)
}

// url returns the parts of the URL the request, given as JSON, would be
// sent to now.
func (h *host) url(request string) map[string]string {
	r := &model.Request{}
	if err := json.Unmarshal([]byte(request), r); err != nil {
		panic(h.vm.NewGoError(err))
	}
	r, _ = vars.ResolveRequest(r, h.in.Scopes)
	u, err := httpx.URL(r)
	if err != nil {
		panic(h.vm.NewGoError(err))
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return map[string]string{"host": u.Host, "path": path, "query": u.RawQuery}
}

// request reads the request back as the script left it.
func (h *host) request(hostObject *goja.Object) (*model.Request, error) {
	export, _ := goja.AssertFunction(hostObject.Get("exportRequest"))
	v, err := export(goja.Undefined())
	if err != nil {
		return h.in.Request, err
	}
	r := &model.Request{}
	if err := json.Unmarshal([]byte(v.String()), r); err != nil {
		return h.in.Request, fmt.Errorf("the %s script left a request that cannot be sent: %w", h.in.Name, err)
	}
	return r, nil
}

// btoa encodes a string of bytes, one per character, as base64.
func btoa(vm *goja.Runtime, s string) goja.Value {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			panic(vm.NewTypeError("btoa: the string has characters outside Latin-1"))
		}
		b = append(b, byte(r))
	}
	return vm.ToValue(base64.StdEncoding.EncodeToString(b))
}

// atob decodes base64 to a string of bytes, one per character.
func atob(vm *goja.Runtime, s string) goja.Value {
	s = strings.TrimRight(strings.Join(strings.Fields(s), ""), "=")
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		panic(vm.NewTypeError("atob: the string is not base64"))
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return vm.ToValue(string(runes))
}

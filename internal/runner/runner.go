// Package runner performs one run of a request: the pre-request script,
// the variables, the request itself, and the post-response script.
package runner

import (
	"context"
	"strings"
	"time"

	"http_gui/internal/httpx"
	"http_gui/internal/model"
	"http_gui/internal/script"
	"http_gui/internal/vars"
)

// Input is what a run needs. The runner works on another goroutine than
// the view's and its scripts write to the scopes, so the caller passes
// copies: vars.Map makes one of each scope.
type Input struct {
	Request       *model.Request
	Scopes        *vars.Scopes // nil for no variables
	Options       httpx.Options
	ScriptTimeout time.Duration // for each script; 0 for script.DefaultTimeout
}

// Result is the outcome of a run.
type Result struct {
	Sent     *model.Request  // the request as it went out; nil if the pre-request script failed
	Response *httpx.Response // nil if there is none
	Missing  []string        // names written as {{name}} that have no value
	// Err is why there is no response: the request could not be sent, or
	// the run was canceled.
	Err error
	// ScriptErr is the error of a script. After the pre-request script's,
	// the request was not sent; after the post-response script's, the
	// response is still here.
	ScriptErr error
	Console   []script.LogLine    // of both scripts, in order
	Tests     []script.TestResult // of both scripts, in order
	// Scopes are the variables as the scripts left them. vars.Apply writes
	// each scope but Runtime back to its rows, to save them.
	Scopes *vars.Scopes
}

// Run performs a run: the pre-request script, which may change the
// variables and the request; the variables, resolved in the request; the
// request, sent; then the post-response script. A request with missing
// variables is still sent, with their {{names}} as written.
func Run(ctx context.Context, in Input) Result {
	scopes := in.Scopes
	if scopes == nil {
		scopes = &vars.Scopes{}
	}
	res := Result{Scopes: scopes}

	// exec runs one of the request's scripts, and reports whether the run
	// goes on.
	exec := func(name string, s model.Script, r *model.Request, resp *httpx.Response) (*model.Request, bool) {
		if strings.TrimSpace(s.Source) == "" {
			return r, true
		}
		out, err := script.Exec(ctx, script.Run{
			Name: name, Script: s, Request: r, Response: resp, Scopes: scopes, Timeout: in.ScriptTimeout,
		})
		res.Console = append(res.Console, out.Console...)
		res.Tests = append(res.Tests, out.Tests...)
		if ctx.Err() != nil {
			res.Err = ctx.Err()
		} else {
			res.ScriptErr = err
		}
		return out.Request, err == nil
	}

	request, ok := exec("pre-request", in.Request.PreScript, in.Request, nil)
	if !ok {
		return res
	}
	res.Sent, res.Missing = vars.ResolveRequest(request, scopes)
	res.Response, res.Err = httpx.Send(ctx, res.Sent, in.Options)
	if res.Err != nil {
		return res
	}
	exec("post-response", in.Request.PostScript, res.Sent, res.Response)
	return res
}

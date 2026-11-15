// Package runner performs one run of a request: it resolves the request's
// variables and sends it. Scripts join it in a later step of the plan.
package runner

import (
	"context"

	"http_gui/internal/httpx"
	"http_gui/internal/model"
	"http_gui/internal/vars"
)

// Input is what a run needs. The runner reads it from another goroutine
// than the view's, so the caller passes copies.
type Input struct {
	Request *model.Request
	Scopes  *vars.Scopes // nil for no variables
	Options httpx.Options
}

// Result is the outcome of a run.
type Result struct {
	Sent     *model.Request  // the request after resolution, as it went out
	Response *httpx.Response // nil when Err is set
	Missing  []string        // names written as {{name}} that have no value
	Err      error
}

// Run resolves the request's variables and sends it. A request with
// missing variables is still sent, with their {{names}} as written.
func Run(ctx context.Context, in Input) Result {
	scopes := in.Scopes
	if scopes == nil {
		scopes = &vars.Scopes{}
	}
	var res Result
	res.Sent, res.Missing = vars.ResolveRequest(in.Request, scopes)
	res.Response, res.Err = httpx.Send(ctx, res.Sent, in.Options)
	return res
}

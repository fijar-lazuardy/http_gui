// Package httpx sends a request of the model with net/http and returns what
// the response pane shows. It knows nothing of variables or scripts: it
// sends the request as given.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"http_gui/internal/model"
)

// Response is a response read to its end.
type Response struct {
	Status     string // "200 OK"
	StatusCode int
	Proto      string
	Headers    http.Header
	Body       []byte
	Size       int64         // of the body, decompressed
	Duration   time.Duration // from sending to the last byte of the body
	FinalURL   string        // after redirects
}

// Options are how requests are sent. The zero value has no timeout, does
// not follow redirects and verifies certificates.
type Options struct {
	Timeout         time.Duration
	FollowRedirects bool
	InsecureTLS     bool
}

// Build makes the HTTP request of r: its URL with the enabled parameters,
// its enabled headers, and its body.
func Build(ctx context.Context, r *model.Request) (*http.Request, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = http.MethodGet
	}

	u, err := URL(r)
	if err != nil {
		return nil, err
	}
	body, contentType, err := buildBody(r.Body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}

	for _, h := range r.Headers {
		if !h.Enabled || h.Key == "" {
			continue
		}
		// net/http sends the Host of the request, not of its headers.
		if strings.EqualFold(h.Key, "Host") {
			req.Host = h.Value
			continue
		}
		req.Header.Add(h.Key, h.Value)
	}
	// The user's Content-Type wins, except over a multipart body, whose
	// boundary only the body's writer knows.
	if contentType != "" && (r.Body.Kind == model.BodyFormData || req.Header.Get("Content-Type") == "") {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// URL returns the URL r is sent to: the one written, with http:// before it
// if it has no scheme, and the enabled parameters after its own query.
func URL(r *model.Request) (*url.URL, error) {
	raw := strings.TrimSpace(r.URL)
	if raw == "" {
		return nil, errors.New("the URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if q := encode(r.Params); q != "" {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += q
	}
	return u, nil
}

// buildBody returns the body's bytes, nil for none, and the content type
// its kind implies.
func buildBody(b model.Body) (io.Reader, string, error) {
	switch b.Kind {
	case model.BodyNone, "":
		return nil, "", nil
	case model.BodyJSON:
		return strings.NewReader(b.Raw), "application/json", nil
	case model.BodyURLEncoded:
		return strings.NewReader(encode(b.Form)), "application/x-www-form-urlencoded", nil
	case model.BodyFormData:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, f := range b.Form {
			if !f.Enabled || f.Key == "" {
				continue
			}
			if err := writePart(w, f); err != nil {
				return nil, "", err
			}
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return &buf, w.FormDataContentType(), nil
	}
	return nil, "", fmt.Errorf("unknown body kind %q", b.Kind)
}

func writePart(w *multipart.Writer, f model.KV) error {
	if !f.File {
		return w.WriteField(f.Key, f.Value)
	}
	file, err := os.Open(f.Value)
	if err != nil {
		return fmt.Errorf("form field %q: %w", f.Key, err)
	}
	defer file.Close()
	part, err := w.CreateFormFile(f.Key, filepath.Base(f.Value))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

// encode writes the enabled rows as a query string, in their order.
func encode(rows []model.KV) string {
	var b strings.Builder
	for _, kv := range rows {
		if !kv.Enabled || kv.Key == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv.Value))
	}
	return b.String()
}

// Send sends r and reads the response to its end. Canceling ctx stops it.
func Send(ctx context.Context, r *model.Request, o Options) (*Response, error) {
	req, err := Build(ctx, r)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := client(o).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
		Proto:      resp.Proto,
		Headers:    resp.Header,
		Body:       body,
		Size:       int64(len(body)),
		Duration:   time.Since(start),
		FinalURL:   resp.Request.URL.String(),
	}, nil
}

// clients holds one client per Options, so that requests sent the same way
// share connections.
var clients sync.Map // Options -> *http.Client

func client(o Options) *http.Client {
	if c, ok := clients.Load(o); ok {
		return c.(*http.Client)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.InsecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &http.Client{Transport: tr, Timeout: o.Timeout}
	if !o.FollowRedirects {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	actual, _ := clients.LoadOrStore(o, c)
	return actual.(*http.Client)
}

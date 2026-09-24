// Package replay sends captured requests to a service again.
package replay

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Bethel-nz/pier/internal/capture"
)

// Result is what the service answered this time.
type Result struct {
	Exchange capture.Exchange // the original
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
	Err      error
}

// BodyChanged reports whether the service answered with a different body.
func (r Result) BodyChanged() bool {
	return r.Err == nil && !r.Exchange.ResponseTruncated && !bytes.Equal(r.Body, r.Exchange.ResponseBody)
}

// hopHeaders describe one connection, not the request; a replay makes its own.
var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length"}

// Client sends replays: it never follows redirects, so the reply compares
// with what the service first said, and it accepts a loopback service's own
// certificate.
func Client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil                                             // the target is loopback; never send it through HTTPS_PROXY
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback targets only
	return &http.Client{
		Transport:     transport,
		Timeout:       time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Send replays e against target, the service's loopback URL. Headers Pier
// redacted when capturing are left out rather than sent as "[redacted]".
func Send(ctx context.Context, client *http.Client, target string, e capture.Exchange) Result {
	result := Result{Exchange: e}
	base, err := url.Parse(target)
	if err != nil {
		result.Err = err
		return result
	}
	destination, err := base.Parse(e.URL)
	if err != nil {
		result.Err = err
		return result
	}
	req, err := http.NewRequestWithContext(ctx, e.Method, destination.String(), bytes.NewReader(e.RequestBody))
	if err != nil {
		result.Err = err
		return result
	}
	for name, values := range e.RequestHeader {
		if len(values) == 1 && values[0] == "[redacted]" {
			continue
		}
		req.Header[name] = append([]string(nil), values...)
	}
	for _, name := range hopHeaders {
		req.Header.Del(name)
	}
	req.Header.Set("X-Forwarded-Host", e.Host)
	req.Header.Set("X-Pier-Replay", strconv.FormatInt(e.ID, 10))

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()
	result.Body, result.Err = io.ReadAll(io.LimitReader(resp.Body, capture.MaxBody))
	result.Status = resp.StatusCode
	result.Header = resp.Header
	result.Duration = time.Since(start)
	return result
}

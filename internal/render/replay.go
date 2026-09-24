package render

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/Bethel-nz/pier/internal/capture"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/replay"
)

// JSONCapture is one captured request in --json output.
type JSONCapture struct {
	ID                int64               `json:"id"`
	Time              time.Time           `json:"time"`
	Service           string              `json:"service"`
	Host              string              `json:"host"`
	Method            string              `json:"method"`
	URL               string              `json:"url"`
	Client            string              `json:"client"`
	Status            int                 `json:"status"`
	DurationMS        int64               `json:"durationMs"`
	RequestHeaders    map[string][]string `json:"requestHeaders,omitempty"`
	RequestBody       string              `json:"requestBody,omitempty"`
	ResponseHeaders   map[string][]string `json:"responseHeaders,omitempty"`
	ResponseBody      string              `json:"responseBody,omitempty"`
	RequestTruncated  bool                `json:"requestTruncated,omitempty"`
	ResponseTruncated bool                `json:"responseTruncated,omitempty"`
}

// JSONReplay is one replay's outcome in --json output.
type JSONReplay struct {
	ID          int64  `json:"id"`
	Method      string `json:"method"`
	URL         string `json:"url"`
	Was         int    `json:"was"`
	Status      int    `json:"status,omitempty"`
	DurationMS  int64  `json:"durationMs,omitempty"`
	BodyChanged bool   `json:"bodyChanged"`
	Body        string `json:"body,omitempty"`
	Error       string `json:"error,omitempty"`
}

func jsonCapture(e capture.Exchange, full bool) JSONCapture {
	out := JSONCapture{
		ID: e.ID, Time: e.Time, Service: e.Service, Host: e.Host, Method: e.Method, URL: e.URL, Client: e.Client,
		Status: e.Status, DurationMS: e.Duration.Milliseconds(),
	}
	if full {
		out.RequestHeaders, out.RequestBody, out.RequestTruncated = e.RequestHeader, string(e.RequestBody), e.RequestTruncated
		out.ResponseHeaders, out.ResponseBody, out.ResponseTruncated = e.ResponseHeader, string(e.ResponseBody), e.ResponseTruncated
	}
	return out
}

// ReplayList prints captured requests, newest first.
func (o Options) ReplayList(proj project.Context, exchanges []capture.Exchange) error {
	if o.JSON {
		items := make([]JSONCapture, len(exchanges))
		for i, e := range exchanges {
			items[i] = jsonCapture(e, false)
		}
		return writeJSON(o.Out, o.Command, proj, map[string]any{"requests": items}, nil, nil)
	}
	if len(exchanges) == 0 {
		fmt.Fprintln(o.Out, "no captured requests yet")
		return nil
	}
	tab := tabwriter.NewWriter(o.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tab, "ID\tWHEN\tSERVICE\tMETHOD\tPATH\tSTATUS\tTOOK")
	for _, e := range exchanges {
		fmt.Fprintf(tab, "%d\t%s\t%s\t%s\t%s\t%d\t%s\n", e.ID, when(e.Time), e.Service, e.Method, shorten(e.URL, 60), e.Status, e.Duration.Round(time.Millisecond))
	}
	_ = tab.Flush()
	fmt.Fprintln(o.Out, "pier replay <id> sends one again; --show prints it")
	return nil
}

// ReplayShow prints each request and the answer it got.
func (o Options) ReplayShow(proj project.Context, exchanges []capture.Exchange) error {
	if o.JSON {
		items := make([]JSONCapture, len(exchanges))
		for i, e := range exchanges {
			items[i] = jsonCapture(e, true)
		}
		return writeJSON(o.Out, o.Command, proj, map[string]any{"requests": items}, nil, nil)
	}
	for i, e := range exchanges {
		if i > 0 {
			fmt.Fprintln(o.Out)
		}
		fmt.Fprintf(o.Out, "#%d  %s  %s via %s from %s\n\n", e.ID, e.Time.Format("2006-01-02 15:04:05"), e.Service, e.Host, e.Client)
		fmt.Fprintf(o.Out, "%s %s\n", e.Method, e.URL)
		writeMessage(o.Out, e.RequestHeader, e.RequestBody, e.RequestTruncated)
		fmt.Fprintf(o.Out, "\n← %d %s in %s\n", e.Status, http.StatusText(e.Status), e.Duration.Round(time.Millisecond))
		writeMessage(o.Out, e.ResponseHeader, e.ResponseBody, e.ResponseTruncated)
	}
	return nil
}

// ReplayResults prints one line per replay: the status before and after.
// It fails when any request could not be sent.
func (o Options) ReplayResults(proj project.Context, results []replay.Result) error {
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
		}
	}
	var err error
	if failed > 0 {
		err = fmt.Errorf("Pier could not send %d of %d requests", failed, len(results))
	}
	if o.JSON {
		items := make([]JSONReplay, len(results))
		for i, r := range results {
			items[i] = JSONReplay{ID: r.Exchange.ID, Method: r.Exchange.Method, URL: r.Exchange.URL, Was: r.Exchange.Status,
				Status: r.Status, DurationMS: r.Duration.Milliseconds(), BodyChanged: r.BodyChanged(), Body: string(r.Body)}
			if r.Err != nil {
				items[i].Error = r.Err.Error()
			}
		}
		if writeErr := writeJSON(o.Out, o.Command, proj, map[string]any{"replays": items}, nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	tab := tabwriter.NewWriter(o.Out, 0, 0, 2, ' ', 0)
	for _, r := range results {
		e := r.Exchange
		if r.Err != nil {
			fmt.Fprintf(tab, "#%d\t%s %s\t\terror: %s\n", e.ID, e.Method, shorten(e.URL, 50), r.Err)
			continue
		}
		status := fmt.Sprintf("%d", r.Status)
		if r.Status != e.Status {
			status = fmt.Sprintf("%d → %d", e.Status, r.Status)
		}
		note := "same body"
		if r.BodyChanged() {
			note = "body changed"
		}
		fmt.Fprintf(tab, "#%d\t%s %s\t%s\t%s\t%s\n", e.ID, e.Method, shorten(e.URL, 50), status, r.Duration.Round(time.Millisecond), note)
	}
	_ = tab.Flush()
	if len(results) == 1 && results[0].Err == nil {
		fmt.Fprintln(o.Out)
		writeMessage(o.Out, nil, results[0].Body, false)
	}
	if err != nil {
		return o.Error(err)
	}
	return nil
}

func writeMessage(w io.Writer, header http.Header, body []byte, truncated bool) {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range header[name] {
			fmt.Fprintf(w, "%s: %s\n", name, value)
		}
	}
	if len(body) == 0 {
		return
	}
	if len(names) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, readable(header, body))
	if truncated {
		fmt.Fprintln(w, "(cut at 1 MB)")
	}
}

// readable shows text bodies as text, unzipping gzip first, and summarizes the rest.
func readable(header http.Header, body []byte) string {
	if strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
		if unzipped, err := gunzip(body); err == nil {
			body = unzipped
		}
	}
	if utf8.Valid(body) && !bytes.ContainsRune(body, 0) {
		return strings.TrimRight(string(body), "\n")
	}
	return fmt.Sprintf("(%d bytes of binary data)", len(body))
}

func gunzip(body []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(reader)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) { // a capped body ends mid-stream
		return nil, err
	}
	return out, nil
}

func when(t time.Time) string {
	if time.Since(t) < 24*time.Hour {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 2 15:04")
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

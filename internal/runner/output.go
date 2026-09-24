package runner

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

var palette = []string{"36", "33", "32", "35", "34", "31"} // cyan, yellow, green, magenta, blue, red

// prefixer interleaves processes' output line by line, each line under its
// process's name, so two servers never split each other's lines.
type prefixer struct {
	mu      sync.Mutex
	out     io.Writer
	color   bool
	width   int
	colors  map[string]string
	pending map[string]*bytes.Buffer
}

func newPrefixer(out io.Writer, names []string, color bool) *prefixer {
	p := &prefixer{out: out, color: color, colors: map[string]string{}, pending: map[string]*bytes.Buffer{}}
	for i, name := range names {
		p.width = max(p.width, len(name))
		p.colors[name] = palette[i%len(palette)]
		p.pending[name] = &bytes.Buffer{}
	}
	return p
}

func (p *prefixer) prefix(name string) string {
	label := name + strings.Repeat(" ", p.width-len(name)) + " │ "
	if p.color {
		return "\x1b[" + p.colors[name] + "m" + label + "\x1b[0m"
	}
	return label
}

type lineWriter struct {
	p    *prefixer
	name string
}

func (p *prefixer) writer(name string) io.Writer { return lineWriter{p: p, name: name} }

func (w lineWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	buf := w.p.pending[w.name]
	buf.Write(b)
	for {
		line, err := buf.ReadBytes('\n')
		if err != nil {
			buf.Write(line) // no newline yet: keep it for the next write
			break
		}
		_, _ = io.WriteString(w.p.out, w.p.prefix(w.name)+string(line))
	}
	return len(b), nil
}

// flush prints a last line that never got its newline.
func (p *prefixer) flush(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if buf := p.pending[name]; buf.Len() > 0 {
		_, _ = io.WriteString(p.out, p.prefix(name)+buf.String()+"\n")
		buf.Reset()
	}
}

// note prints Pier's own line about a process, set apart from its output.
func (p *prefixer) note(name, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.color {
		message = "\x1b[2m" + message + "\x1b[0m"
	}
	_, _ = io.WriteString(p.out, p.prefix(name)+"pier: "+message+"\n")
}

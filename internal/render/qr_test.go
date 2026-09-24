package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"rsc.io/qr"
)

func TestQRIsASquareOfBlockCharacters(t *testing.T) {
	var out strings.Builder
	if err := QR(&out, "https://demo.local/", false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	width := utf8.RuneCountInString(lines[0])
	// Each text row holds two module rows, so the drawing is about half as tall as wide.
	if width < 25 || len(lines) < width/2 || len(lines) > width/2+1 {
		t.Fatalf("QR is %d wide and %d lines tall", width, len(lines))
	}
	for i, line := range lines {
		if utf8.RuneCountInString(line) != width {
			t.Fatalf("line %d has width %d, want %d", i, utf8.RuneCountInString(line), width)
		}
		if strings.Trim(line, " █▀▄") != "" {
			t.Fatalf("line %d has characters other than blocks: %q", i, line)
		}
	}
}

func TestQRColorModeResetsEveryLine(t *testing.T) {
	var out strings.Builder
	if err := QR(&out, "https://demo.local/", true); err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "\x1b[30;107m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d does not set and reset its colors: %q", i, line)
		}
	}
}

// Reading the printed blocks back must give exactly the encoder's modules, so
// a phone camera sees the same code.
func TestQRDrawingMatchesTheEncodedModules(t *testing.T) {
	const text = "https://pier-demo.local/"
	for _, color := range []bool{false, true} {
		var out strings.Builder
		if err := QR(&out, text, color); err != nil {
			t.Fatal(err)
		}
		plain := strings.NewReplacer("\x1b[30;107m", "", "\x1b[0m", "").Replace(out.String())
		code, _ := qr.Encode(text, qr.M)
		rows := strings.Split(strings.TrimRight(plain, "\n"), "\n")
		for row, line := range rows {
			for col, r := range []rune(line) {
				top := r == '█' || r == '▀'
				bottom := r == '█' || r == '▄'
				if !color { // light modules are drawn on dark terminals
					top, bottom = !top, !bottom
				}
				x, y := col-qrQuiet, row*2-qrQuiet
				if want := inCode(code, x, y); top != want {
					t.Fatalf("color=%v module (%d,%d) = %v, want %v", color, x, y, top, want)
				}
				if want := inCode(code, x, y+1); bottom != want {
					t.Fatalf("color=%v module (%d,%d) = %v, want %v", color, x, y+1, bottom, want)
				}
			}
		}
	}
}

func inCode(code *qr.Code, x, y int) bool {
	return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
}

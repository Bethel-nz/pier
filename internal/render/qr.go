package render

import (
	"fmt"
	"io"
	"strings"

	"rsc.io/qr"
)

const qrQuiet = 2 // modules of white border; phone cameras need some

// QR prints text as a scannable QR code using half-block characters, two
// modules per character row. With color it paints black on white explicitly,
// so it scans on light and dark terminals alike. Without color it assumes a
// dark terminal and draws the light modules.
func QR(w io.Writer, text string, color bool) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return fmt.Errorf("Pier could not draw a QR code: %w", err)
	}
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	var b strings.Builder
	for y := -qrQuiet; y < code.Size+qrQuiet; y += 2 {
		if color {
			b.WriteString("\x1b[30;107m")
		}
		for x := -qrQuiet; x < code.Size+qrQuiet; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			if !color {
				// Dark terminal: draw light modules as blocks.
				top, bottom = !top, !bottom
			}
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if color {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

package devconsole

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// qrSVG renders text as an inline SVG QR code (white background, four
// module quiet zone) drawn with one path, sized px by px.
func qrSVG(text string, px int) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := code.Size + 2*quiet
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			// Merge horizontal runs into one rectangle.
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run - 1
		}
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges" role="img" aria-label="接入凭据二维码" class="mx-auto rounded-md"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#0a0b0a"/></svg>`, n, n, px, px, n, n, path.String())
	return template.HTML(svg), nil
}

package library

import (
	"strings"
	"testing"
)

// Intent (the library is served to the phone over Tailscale): slide Markdown
// cannot inject script into the deck player.
func TestSlideHTMLEscapes(t *testing.T) {
	got := SlideHTML("## Plan <script>alert(1)</script>\n\n- [click](javascript:alert(1))\n- **bold** and [docs](https://example.com)")
	for _, bad := range []string{"<script>", `href="javascript:`} {
		if strings.Contains(got, bad) {
			t.Errorf("found %q in:\n%s", bad, got)
		}
	}
	for _, want := range []string{"<h2>Plan &lt;script&gt;", "<strong>bold</strong>", `<a href="https://example.com">docs</a>`, "<li>click"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The player's slide data is JSON inside a script tag; a slide title cannot
// close that tag.
func TestPlayerDataCannotCloseTheScript(t *testing.T) {
	page := RenderPlayer("Deck", []PlayerSlide{{Title: "</script><b>x", HTML: "<p>ok</p>", Seconds: 1}})
	if strings.Contains(page, "</script><b>") {
		t.Error("a title closed the script tag")
	}
}

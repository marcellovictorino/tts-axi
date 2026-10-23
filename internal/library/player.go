package library

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"regexp"
	"strings"
)

// PlayerSlide is one slide as the player shows it.
type PlayerSlide struct {
	Title   string  `json:"title"`
	HTML    string  `json:"html"`            // the slide body as HTML (when no image)
	Image   string  `json:"image,omitempty"` // a picture of the slide, relative to the page
	Audio   string  `json:"audio,omitempty"` // "" for a silent slide
	Seconds float64 `json:"seconds"`         // how long the slide stays up
}

// Timing is one row of timings.json.
type Timing struct {
	Slide   int     `json:"slide"`
	Title   string  `json:"title"`
	Start   float64 `json:"start"`
	Seconds float64 `json:"seconds"`
	Audio   string  `json:"audio,omitempty"`
}

var playerTmpl = template.Must(template.New("player").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; background: #0e0e10; color: #f2f2f4; font: 18px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  body { display: flex; flex-direction: column; }
  #stage { flex: 1; display: flex; align-items: center; justify-content: center; padding: 3vh 4vw; min-height: 0; }
  #slide { width: min(1100px, 100%); aspect-ratio: 16 / 9; max-height: 100%; background: #fafafa; color: #1d1d1f; border-radius: 10px; padding: 5% 6%; overflow: hidden; display: flex; flex-direction: column; justify-content: center; }
  #slide.image { padding: 0; background: #000; }
  #slide img.full { width: 100%; height: 100%; object-fit: contain; }
  #slide h1 { font-size: 2.2em; margin: 0 0 .4em; } #slide h2 { font-size: 1.7em; margin: 0 0 .4em; } #slide h3 { font-size: 1.3em; }
  #slide ul, #slide ol { font-size: 1.15em; } #slide p { font-size: 1.15em; }
  #slide img { max-width: 100%; max-height: 50vh; }
  #slide code { background: #eceef1; padding: 0 .25em; border-radius: 4px; }
  nav { display: flex; gap: 10px; align-items: center; padding: 10px 16px 16px; flex-wrap: wrap; justify-content: center; }
  button { font: inherit; font-size: 15px; background: #2a2a2e; color: inherit; border: 1px solid #3a3a40; border-radius: 8px; padding: 8px 14px; cursor: pointer; }
  button:hover { background: #35353a; }
  #count { font-size: 14px; color: #a1a1a6; min-width: 9em; text-align: center; }
  #bar { height: 3px; background: #2a2a2e; } #fill { height: 100%; width: 0; background: #4c9bff; }
</style>
</head>
<body>
<div id="bar"><div id="fill"></div></div>
<div id="stage"><div id="slide"></div></div>
<nav>
  <button id="prev" title="Previous slide (←)">◀ Previous</button>
  <button id="play" title="Play or pause (space)">▶ Play</button>
  <button id="next" title="Next slide (→)">Next ▶</button>
  <span id="count"></span>
</nav>
<audio id="audio" preload="auto"></audio>
<script>
const slides = {{.SlidesJSON}};
const gap = 600; // ms of stillness between a clip's end and the next slide
let i = 0, playing = false, timer = null;
const el = id => document.getElementById(id);
const audio = el("audio");
function show(n) {
  i = Math.max(0, Math.min(slides.length - 1, n));
  const s = slides[i], box = el("slide");
  box.className = s.image ? "image" : "";
  box.innerHTML = s.image ? '<img class="full" alt="">' : s.html;
  if (s.image) box.querySelector("img").src = s.image;
  el("count").textContent = "Slide " + (i + 1) + " of " + slides.length;
  el("fill").style.width = ((i + 1) / slides.length * 100) + "%";
  clearTimeout(timer);
  audio.pause();
  if (s.audio) { audio.src = s.audio; } else { audio.removeAttribute("src"); }
  if (playing) start();
}
function start() {
  const s = slides[i];
  if (s.audio) { audio.currentTime = 0; audio.play().catch(() => setPlaying(false)); }
  else { timer = setTimeout(advance, s.seconds * 1000); }
}
function advance() {
  if (i < slides.length - 1) { show(i + 1); } else { setPlaying(false); }
}
function setPlaying(p) {
  playing = p;
  el("play").textContent = p ? "❚❚ Pause" : "▶ Play";
  if (p) { start(); } else { audio.pause(); clearTimeout(timer); }
}
audio.addEventListener("ended", () => { if (playing) timer = setTimeout(advance, gap); });
el("play").onclick = () => setPlaying(!playing);
el("next").onclick = () => show(i + 1);
el("prev").onclick = () => show(i - 1);
document.addEventListener("keydown", e => {
  if (e.key === "ArrowRight") show(i + 1);
  else if (e.key === "ArrowLeft") show(i - 1);
  else if (e.key === " ") { e.preventDefault(); setPlaying(!playing); }
});
show(0);
</script>
</body>
</html>
`))

// RenderPlayer writes the auto-advancing player page for a deck.
func RenderPlayer(title string, slides []PlayerSlide) string {
	data, err := json.Marshal(slides)
	if err != nil {
		panic(err)
	}
	var b bytes.Buffer
	// template.JS marks the JSON as safe to place in the script: it came
	// from json.Marshal, which escapes <, > and & inside strings.
	err = playerTmpl.Execute(&b, map[string]any{"Title": title, "SlidesJSON": template.JS(data)})
	if err != nil {
		panic(err)
	}
	return b.String()
}

// SlideHTML converts a slide's Markdown to simple HTML: headings, lists,
// paragraphs, emphasis, code, links and images. Everything else is shown as
// text. It is deliberately small; for full fidelity export slide pictures and
// pass them with --images.
func SlideHTML(md string) string {
	var out strings.Builder
	var para []string
	list := ""
	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			out.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flushPara()
			closeList()
		case headRE.MatchString(line):
			flushPara()
			closeList()
			m := headRE.FindStringSubmatch(line)
			n := len(m[1])
			out.WriteString("<h" + string(rune('0'+n)) + ">" + inline(m[2]) + "</h" + string(rune('0'+n)) + ">\n")
		case bulletRE.MatchString(line) || numberRE.MatchString(line):
			flushPara()
			kind := "ul"
			re := bulletRE
			if numberRE.MatchString(line) {
				kind, re = "ol", numberRE
			}
			if list != kind {
				closeList()
				out.WriteString("<" + kind + ">\n")
				list = kind
			}
			out.WriteString("<li>" + inline(re.ReplaceAllString(line, "")) + "</li>\n")
		default:
			closeList()
			para = append(para, line)
		}
	}
	flushPara()
	closeList()
	return out.String()
}

var (
	headRE   = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*$`)
	bulletRE = regexp.MustCompile(`^[-*+]\s+`)
	numberRE = regexp.MustCompile(`^\d+[.)]\s+`)
	imgRE    = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)[^)]*\)`)
	aRE      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)[^)]*\)`)
	codeRE   = regexp.MustCompile("`([^`]+)`")
	strongRE = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	emRE     = regexp.MustCompile(`\*([^*]+)\*|\b_([^_]+)_\b`)
)

// inline escapes text, then turns Markdown inline marks into tags. Links and
// image sources are kept only when they are http(s) or relative paths.
func inline(s string) string {
	s = html.EscapeString(s)
	s = imgRE.ReplaceAllStringFunc(s, func(m string) string {
		p := imgRE.FindStringSubmatch(m)
		if !safeURL(p[2]) {
			return p[1]
		}
		return `<img alt="` + p[1] + `" src="` + p[2] + `">`
	})
	s = aRE.ReplaceAllStringFunc(s, func(m string) string {
		p := aRE.FindStringSubmatch(m)
		if !safeURL(p[2]) {
			return p[1]
		}
		return `<a href="` + p[2] + `">` + p[1] + `</a>`
	})
	s = codeRE.ReplaceAllString(s, "<code>$1</code>")
	s = strongRE.ReplaceAllString(s, "<strong>$1$2</strong>")
	s = emRE.ReplaceAllString(s, "<em>$1$2</em>")
	return s
}

func safeURL(u string) bool {
	l := strings.ToLower(u)
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return true
	}
	return !strings.Contains(l, ":") // relative paths only; no javascript: and friends
}

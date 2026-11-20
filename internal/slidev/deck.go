// Package slidev narrates Slidev decks: it reads a slides.md the way Slidev
// does, splits each slide's presenter notes at [click] markers, writes the
// cue times back into a copy of the deck with tts-axi's narration layer, and
// records the narrated deck to an MP4 in a hidden Chrome.
package slidev

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Slide is one Slidev slide.
type Slide struct {
	// Title is the frontmatter title, else the first heading, else "Slide N".
	Title string
	// Steps is the narration: Steps[0] plays when the slide opens, Steps[k]
	// after its k-th click. Raw note text; nil for a slide without notes.
	Steps []string
	// Hidden slides (hide or disabled in the frontmatter) are not shown by
	// Slidev, so they are not narrated.
	Hidden bool

	from, to int    // the slide's lines in the source, [from, to)
	raw      string // those lines, frontmatter included
	front    bool   // raw starts with a --- frontmatter block
}

// Deck is a parsed slides.md.
type Deck struct {
	Title      string // headmatter title
	Lang       string // headmatter lang
	Theme      string // headmatter theme
	Transition string // headmatter transition
	Slides     []Slide

	lines []string // the source, for Render
}

var (
	// A frontmatter block runs from the opening --- line to the next line
	// that is only ---; dashes inside a value do not close it.
	frontRE = regexp.MustCompile(`^---.*\n((?:.*\n)*?)---[ \t]*(?:\n|$)`)
	yamlRE  = regexp.MustCompile("^\\s*```ya?ml")
	// The note is the last comment, when it ends the slide, as Slidev reads it.
	commentRE = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	headingRE = regexp.MustCompile(`(?m)^(#+) (.*)$`)
	codeRE    = regexp.MustCompile("(?ms)^```.+?^```")
	clickRE   = regexp.MustCompile(`(?i)\[click(:[^\]]*)?\]`)
	keyRE     = regexp.MustCompile(`^([A-Za-z_][\w-]*)\s*:\s*(.*)$`)
)

// Parse reads slides.md with Slidev's own slicing rules (@slidev/parser): a
// line starting with --- ends a slide; when the next line is not blank, the
// lines up to the next --- are the new slide's frontmatter. Code blocks and
// HTML comments are skipped while looking for separators.
func Parse(src string) (Deck, error) {
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), "\ufeff")
	lines := strings.Split(src, "\n")
	d := Deck{lines: lines}
	start := 0
	slice := func(end int) {
		if start == end {
			return
		}
		raw := strings.Join(lines[start:end], "\n")
		d.Slides = append(d.Slides, Slide{from: start, to: end, raw: raw, front: frontRE.MatchString(raw)})
		start = end + 1
	}
	inComment := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if inComment {
			inComment = commentState(lines[i], true)
			continue
		}
		switch {
		case strings.HasPrefix(line, "---"):
			slice(i)
			if (len(line) == 3 || line[3] != '-') && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
				// Frontmatter: the --- line and the block belong to the new slide.
				start = i
				for i++; i < len(lines); i++ {
					if strings.TrimRight(lines[i], " \t") == "---" {
						break
					}
				}
			}
		case strings.HasPrefix(strings.TrimLeft(line, " \t"), "```"):
			fence := strings.TrimLeft(line, " \t")
			fence = line[:len(line)-len(fence)] + fence[:len(fence)-len(strings.TrimLeft(fence, "`"))]
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], fence) {
					break
				}
			}
			if j != len(lines) {
				i = j
			}
		default:
			inComment = commentState(lines[i], false)
		}
	}
	if start <= len(lines)-1 {
		slice(len(lines))
	}
	if len(d.Slides) == 0 {
		return d, fmt.Errorf("no slides found")
	}
	for i := range d.Slides {
		if err := d.Slides[i].read(i + 1); err != nil {
			return d, err
		}
	}
	head := d.Slides[0].frontmatter()
	d.Title, d.Lang, d.Theme, d.Transition = head["title"], strings.ToLower(head["lang"]), head["theme"], head["transition"]
	if d.Title == "" {
		d.Title = d.Slides[0].Title
	}
	narrated := 0
	for _, s := range d.Slides {
		if !s.Hidden && len(s.Steps) > 0 {
			narrated++
		}
	}
	if narrated == 0 {
		return d, fmt.Errorf("no presenter notes found: end each slide with an HTML comment, <!-- like this -->, and mark each animation step with [click]")
	}
	return d, nil
}

// commentState follows <!-- --> across a line, as Slidev's parser does.
func commentState(line string, in bool) bool {
	for line != "" {
		if in {
			end := strings.Index(line, "-->")
			if end < 0 {
				return true
			}
			in, line = false, line[end+3:]
			continue
		}
		start := strings.Index(line, "<!--")
		if start < 0 {
			return false
		}
		end := strings.Index(line[start+4:], "-->")
		if end < 0 {
			return true
		}
		line = line[start+4+end+3:]
	}
	return in
}

// read fills the title, steps and hidden flag from the slide's raw text.
func (s *Slide) read(n int) error {
	content := s.raw
	if s.front {
		content = s.raw[len(frontRE.FindString(s.raw)):]
	} else if yamlRE.MatchString(s.raw) {
		return fmt.Errorf("slide %d sets its frontmatter in a yaml code block; use a --- block so tts-axi can add the narration cues", n)
	}
	fm := s.frontmatter()
	if fm["src"] != "" {
		return fmt.Errorf("slide %d imports slides from %s; copy them into slides.md to narrate them", n, fm["src"])
	}
	s.Hidden = fm["hide"] == "true" || fm["disabled"] == "true"
	content = strings.TrimSpace(content)
	if s.Hidden {
		// Slidev does not show it, so its notes are never spoken or checked.
		s.Title = fmt.Sprintf("Slide %d", n)
		return nil
	}
	if ms := commentRE.FindAllStringSubmatchIndex(content, -1); len(ms) > 0 {
		last := ms[len(ms)-1]
		if last[1] >= len(content) {
			note := strings.TrimSpace(content[last[2]:last[3]])
			content = strings.TrimSpace(content[:last[0]])
			if note != "" {
				steps, err := splitClicks(note)
				if err != nil {
					return fmt.Errorf("slide %d: %w", n, err)
				}
				s.Steps = steps
			}
		}
	}
	switch {
	case fm["title"] != "":
		s.Title = fm["title"]
	case fm["name"] != "":
		s.Title = fm["name"]
	default:
		if m := headingRE.FindStringSubmatch(codeRE.ReplaceAllString(content, "")); m != nil {
			s.Title = strings.TrimSpace(tagRE.ReplaceAllString(m[2], ""))
		}
	}
	if s.Title == "" {
		s.Title = fmt.Sprintf("Slide %d", n)
	}
	return nil
}

var tagRE = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

// splitClicks cuts a note at its [click] markers.
func splitClicks(note string) ([]string, error) {
	var steps []string
	last := 0
	for _, m := range clickRE.FindAllStringSubmatchIndex(note, -1) {
		if m[2] >= 0 {
			return nil, fmt.Errorf("%s is not supported; put a plain [click] before each step's words", note[m[0]:m[1]])
		}
		steps = append(steps, strings.TrimSpace(note[last:m[0]]))
		last = m[1]
	}
	return append(steps, strings.TrimSpace(note[last:])), nil
}

// frontmatter reads the slide's top-level scalar keys; nested values are
// left out, which is all tts-axi needs.
func (s Slide) frontmatter() map[string]string {
	out := map[string]string{}
	if !s.front {
		return out
	}
	for _, l := range strings.Split(frontRE.FindStringSubmatch(s.raw)[1], "\n") {
		if m := keyRE.FindStringSubmatch(l); m != nil {
			out[m[1]] = unquote(strings.TrimSpace(stripComment(m[2])))
		}
	}
	return out
}

func stripComment(v string) string {
	if i := strings.Index(v, " #"); i >= 0 && !strings.ContainsAny(v[:i], `"'`) {
		return v[:i]
	}
	return v
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// Cues is one narrated slide's timing: when it opens, in seconds into the
// deck's audio, and when each click fires, in seconds after it opens.
type Cues struct {
	Start  float64
	Clicks []float64
}

// Render returns the deck with each timed slide's start, cues and clicks set
// in its frontmatter; every other line is kept as it was. Slides without an
// entry in cues (hidden slides) are left alone.
func (d Deck) Render(cues map[int]Cues) string {
	out := make([]string, 0, len(d.lines)+3*len(cues))
	at := 0 // the next source line to copy
	for i, s := range d.Slides {
		out = append(out, d.lines[at:s.from]...) // separators between slides
		at = s.to
		c, ok := cues[i]
		if !ok {
			out = append(out, d.lines[s.from:s.to]...)
			continue
		}
		keys := []string{
			"start: " + num(c.Start),
			"cues: [" + nums(c.Clicks) + "]",
			"clicks: " + strconv.Itoa(len(c.Clicks)),
		}
		if !s.front {
			// The separator before the slide opens its new frontmatter; the
			// first slide has none, so it gets one.
			if len(out) == 0 || strings.TrimRight(out[len(out)-1], " \t") != "---" {
				out = append(out, "---")
			}
			out = append(out, keys...)
			out = append(out, "---")
			out = append(out, d.lines[s.from:s.to]...)
			continue
		}
		m := frontRE.FindStringSubmatchIndex(s.raw)
		out = append(out, strings.Split(s.raw[:m[2]]+setKeys(s.raw[m[2]:m[3]], keys)+s.raw[m[3]:], "\n")...)
	}
	out = append(out, d.lines[at:]...)
	return strings.Join(out, "\n")
}

// setKeys drops any existing start, cues or clicks (with their indented
// continuation lines) and appends the new values.
func setKeys(block string, keys []string) string {
	var out []string
	skipping := false
	for _, l := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
		if skipping && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "-")) {
			continue
		}
		skipping = false
		if m := keyRE.FindStringSubmatch(l); m != nil && (m[1] == "start" || m[1] == "cues" || m[1] == "clicks") {
			skipping = true
			continue
		}
		out = append(out, l)
	}
	if len(out) == 1 && out[0] == "" {
		out = nil
	}
	return strings.Join(append(out, keys...), "\n") + "\n"
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func nums(vs []float64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = num(v)
	}
	return strings.Join(parts, ", ")
}

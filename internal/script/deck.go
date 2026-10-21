package script

import (
	"fmt"
	"regexp"
	"strings"
)

// Slide is one slide of a deck.
type Slide struct {
	Number int    // 1-based
	Title  string // first heading, or "Slide N"
	Body   string // the slide's Markdown, speaker notes removed
	Notes  string // the narration, as plain text; "" for a silent slide
}

// Deck is a parsed slide deck.
type Deck struct {
	Title  string
	Lang   string
	Voices map[string]string
	Slides []Slide
}

var (
	separatorRE = regexp.MustCompile(`(?m)^---\s*$`)
	noteRE      = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	// Marp directives look like <!-- _class: lead --> or <!-- paginate: true -->;
	// they are settings, not narration.
	directiveRE = regexp.MustCompile(`^\s*_?(theme|paginate|header|footer|class|backgroundColor|backgroundImage|backgroundPosition|backgroundRepeat|backgroundSize|color|size|style|headingDivider|math|marp|lang|title|description|author|transition|layout)\s*:.*$`)
	headingRE   = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*$`)
)

// ParseDeck reads Markdown slides (Marp or Slidev style): an optional front
// matter block, then slides separated by lines of "---". Every HTML comment on
// a slide that is not a Marp directive is narration for that slide.
func ParseDeck(src string) (Deck, error) {
	sc, body, err := frontMatter(src)
	if err != nil {
		return Deck{}, err
	}
	d := Deck{Title: sc.Title, Lang: sc.Lang, Voices: sc.Voices}
	for _, part := range separatorRE.Split(body, -1) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		s := Slide{Number: len(d.Slides) + 1}
		var notes []string
		for _, m := range noteRE.FindAllStringSubmatch(part, -1) {
			text := strings.TrimSpace(m[1])
			if text == "" || (!strings.Contains(text, "\n") && directiveRE.MatchString(text)) {
				continue
			}
			notes = append(notes, text)
		}
		s.Body = strings.TrimSpace(noteRE.ReplaceAllString(part, ""))
		var paras []string
		for _, p := range paragraphs(strings.Join(notes, "\n\n")) {
			if c := Clean(p); c != "" {
				paras = append(paras, c)
			}
		}
		s.Notes = strings.Join(paras, "\n\n")
		if m := headingRE.FindStringSubmatch(s.Body); m != nil {
			s.Title = Clean(m[1])
		} else {
			s.Title = fmt.Sprintf("Slide %d", s.Number)
		}
		if s.Body == "" && s.Notes == "" {
			continue
		}
		d.Slides = append(d.Slides, s)
	}
	if len(d.Slides) == 0 {
		return d, fmt.Errorf("no slides found: separate slides with a line of ---")
	}
	if d.Title == "" {
		d.Title = d.Slides[0].Title
	}
	narrated := 0
	for _, s := range d.Slides {
		if s.Notes != "" {
			narrated++
		}
	}
	if narrated == 0 {
		return d, fmt.Errorf("no speaker notes found: put each slide's narration in an HTML comment, <!-- like this -->")
	}
	return d, nil
}

// NotesScript turns one slide's notes into segments, honouring [pause] marks
// and paragraphs.
func NotesScript(notes string) []Segment {
	var sc Script
	for _, p := range paragraphs(notes) {
		appendSpeech(&sc, "", p, -1)
	}
	return sc.Segments
}

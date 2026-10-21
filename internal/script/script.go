// Package script turns the three inputs tts-axi reads into a flat list of
// things to speak and silences to insert:
//
//   - plain text or Markdown (say): one segment per paragraph;
//   - a multi-voice script (render): "## Chapter" headings, "Speaker: line"
//     lines and [pause 1.5s] markers;
//   - a slide deck (deck): Markdown slides split by "---" with the narration
//     in each slide's speaker notes (HTML comments, as Marp and Slidev write
//     them).
//
// Phonon reads plain text only and has no pause or emotion markup, so pauses
// become real silence that tts-axi inserts when joining the audio.
package script

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Segment is one piece of the output, in order.
type Segment struct {
	// Speaker is the name the script gave ("" for the narrator). Pause
	// segments have no speaker.
	Speaker string
	Text    string        // what to say; empty for a pause
	Pause   time.Duration // silence to insert; zero for speech
	Chapter int           // index into Script.Chapters, -1 before the first heading
}

// IsPause reports whether the segment is silence.
func (s Segment) IsPause() bool { return s.Text == "" }

// Script is a parsed input.
type Script struct {
	Title    string
	Lang     string            // from the front matter; "" when not given
	Voices   map[string]string // speaker -> voice name, from the front matter
	Chapters []string
	Segments []Segment
}

var (
	pauseRE   = regexp.MustCompile(`(?i)\[\s*pause(?:\s+(\d+(?:\.\d+)?)\s*(ms|s|sec|seconds?)?)?\s*\]`)
	speakerRE = regexp.MustCompile(`^([\p{Lu}][\p{L}\p{M}0-9 .'’-]{0,40}?)\s*:\s+(\S.*)$`)
	chapterRE = regexp.MustCompile(`^#{1,2}\s+(.+?)\s*#*$`)
	titleRE   = regexp.MustCompile(`^#\s+(.+?)\s*#*$`)
)

// DefaultPause is the silence a bare [pause] inserts.
const DefaultPause = time.Second

// ParseText reads plain text or Markdown for `say`: front matter is honoured,
// and every paragraph becomes one speech segment.
func ParseText(src string) (Script, error) {
	sc, body, err := frontMatter(src)
	if err != nil {
		return sc, err
	}
	for _, para := range paragraphs(body) {
		if sc.Title == "" {
			if m := titleRE.FindStringSubmatch(para); m != nil {
				sc.Title = Clean(m[1])
			}
		}
		appendSpeech(&sc, "", para, -1)
	}
	if len(speech(sc.Segments)) == 0 {
		return sc, fmt.Errorf("nothing to say: the text is empty")
	}
	return sc, nil
}

// ParseScript reads a multi-voice script for `render`.
//
// A "# Title" line names the episode; "## Heading" lines start chapters; a
// line "Name: words" starts that speaker's turn and following lines continue
// it until a blank line, a heading or another speaker; [pause 2s] inserts
// silence anywhere.
func ParseScript(src string) (Script, error) {
	sc, body, err := frontMatter(src)
	if err != nil {
		return sc, err
	}
	chapter := -1
	speaker := ""
	var turn []string
	flush := func() {
		if len(turn) > 0 {
			appendSpeech(&sc, speaker, strings.Join(turn, " "), chapter)
			turn = nil
		}
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
			continue
		case strings.HasPrefix(line, "## ") || (strings.HasPrefix(line, "# ") && sc.Title != ""):
			flush()
			speaker = ""
			m := chapterRE.FindStringSubmatch(line)
			sc.Chapters = append(sc.Chapters, Clean(m[1]))
			chapter = len(sc.Chapters) - 1
			continue
		case strings.HasPrefix(line, "# "):
			flush()
			sc.Title = Clean(titleRE.FindStringSubmatch(line)[1])
			continue
		case strings.HasPrefix(line, "<!--"):
			continue
		}
		if m := speakerRE.FindStringSubmatch(line); m != nil && !looksLikeTime(m[1]) {
			flush()
			speaker = strings.TrimSpace(m[1])
			line = m[2]
		}
		turn = append(turn, line)
	}
	flush()
	if len(speech(sc.Segments)) == 0 {
		return sc, fmt.Errorf("nothing to say: the script has no lines")
	}
	return sc, nil
}

// looksLikeTime rejects "At 10" in "At 10:30 we meet" style lines... and any
// label that ends in a digit, which a speaker name never does.
func looksLikeTime(label string) bool {
	return label != "" && label[len(label)-1] >= '0' && label[len(label)-1] <= '9'
}

// appendSpeech splits text at pause markers and appends speech and pause
// segments.
func appendSpeech(sc *Script, speaker, text string, chapter int) {
	last := 0
	for _, m := range pauseRE.FindAllStringSubmatchIndex(text, -1) {
		if t := Clean(text[last:m[0]]); t != "" {
			sc.Segments = append(sc.Segments, Segment{Speaker: speaker, Text: t, Chapter: chapter})
		}
		d := DefaultPause
		if m[2] >= 0 {
			v, _ := strconv.ParseFloat(text[m[2]:m[3]], 64)
			unit := ""
			if m[4] >= 0 {
				unit = strings.ToLower(text[m[4]:m[5]])
			}
			if unit == "ms" {
				d = time.Duration(v * float64(time.Millisecond))
			} else {
				d = time.Duration(v * float64(time.Second))
			}
		}
		if d > 0 {
			sc.Segments = append(sc.Segments, Segment{Pause: d, Chapter: chapter})
		}
		last = m[1]
	}
	if t := Clean(text[last:]); t != "" {
		sc.Segments = append(sc.Segments, Segment{Speaker: speaker, Text: t, Chapter: chapter})
	}
}

func speech(segs []Segment) []Segment {
	var out []Segment
	for _, s := range segs {
		if !s.IsPause() {
			out = append(out, s)
		}
	}
	return out
}

// paragraphs splits on blank lines and joins each paragraph's lines.
func paragraphs(body string) []string {
	var out []string
	var cur []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, " "))
				cur = nil
			}
			continue
		}
		// A heading or list item is its own paragraph, even without a blank line.
		if len(cur) > 0 && (strings.HasPrefix(line, "#") || listRE.MatchString(line)) {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}
	return out
}

var (
	commentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	imageRE   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRE    = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	tagRE     = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	listRE    = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+`)
	emphRE    = regexp.MustCompile(`(\*\*|__|\*|_|~~|` + "`" + `)([^*_~` + "`" + `]+)(\*\*|__|\*|_|~~|` + "`" + `)`)
	spaceRE   = regexp.MustCompile(`\s+`)
)

// Clean turns a line of Markdown into the plain text Phonon should read:
// comments, images, link targets, HTML tags, emphasis marks, heading and list
// markers are removed.
func Clean(s string) string {
	s = commentRE.ReplaceAllString(s, " ")
	s = imageRE.ReplaceAllString(s, " ")
	s = linkRE.ReplaceAllString(s, "$1")
	s = tagRE.ReplaceAllString(s, " ")
	s = strings.TrimLeft(strings.TrimSpace(s), "#> ")
	s = listRE.ReplaceAllString(s, "")
	for i := 0; i < 3; i++ { // nested emphasis such as ***word***
		s = emphRE.ReplaceAllString(s, "$2")
	}
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

// frontMatter splits an optional "---" block at the top of the file. It
// understands the few keys tts-axi uses: title, lang, and voices (either
// inline as {Host: Hugh, Guest: Bryony} or as indented "Host: Hugh" lines).
func frontMatter(src string) (Script, string, error) {
	sc := Script{Voices: map[string]string{}}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return sc, src, nil
	}
	rest := src[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return sc, src, nil
	}
	block, body := rest[:end], rest[end+4:]
	body = afterFirstLine(body) // the rest of the closing "---" line
	inVoices := false
	for _, raw := range strings.Split(block, "\n") {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		indented := strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")
		key, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		if indented && inVoices {
			sc.Voices[key] = val
			continue
		}
		inVoices = false
		switch strings.ToLower(key) {
		case "title":
			sc.Title = val
		case "lang", "language":
			sc.Lang = strings.ToLower(val)
		case "voices":
			if strings.HasPrefix(val, "{") {
				for _, pair := range strings.Split(strings.Trim(val, "{}"), ",") {
					k, v, ok := strings.Cut(pair, ":")
					if ok {
						sc.Voices[strings.TrimSpace(unquote(strings.TrimSpace(k)))] = unquote(strings.TrimSpace(v))
					}
				}
			} else if val == "" {
				inVoices = true
			} else {
				return sc, body, fmt.Errorf("front matter: voices takes a mapping such as {Host: Hugh, Guest: Bryony}")
			}
		}
	}
	return sc, body, nil
}

func afterFirstLine(s string) string {
	_, after, _ := strings.Cut(s, "\n")
	return after
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

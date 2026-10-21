package script

import (
	"testing"
	"time"
)

// Intent (spec: render reads "## chapter" headings, "Speaker: line" and
// [pause Ns]): the example script from the plan parses into chapters,
// speaker turns and a real pause.
func TestParseScript(t *testing.T) {
	src := `---
title: Your day ahead
lang: en
voices:
  Host: Hugh
  Guest: Bryony
---
## Calendar
Host: Morning. Three things today,
the first at 10:30.
[pause 1.5s]
Guest: First, the **ten o'clock** review. [pause 500ms] Then lunch.

## Slack
Host: Two threads need you.
`
	sc, err := ParseScript(src)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Title != "Your day ahead" || sc.Lang != "en" || sc.Voices["Host"] != "Hugh" || sc.Voices["Guest"] != "Bryony" {
		t.Errorf("front matter: %+v", sc)
	}
	if len(sc.Chapters) != 2 || sc.Chapters[1] != "Slack" {
		t.Errorf("chapters %v", sc.Chapters)
	}
	want := []Segment{
		{Speaker: "Host", Text: "Morning. Three things today, the first at 10:30.", Chapter: 0},
		{Pause: 1500 * time.Millisecond, Chapter: 0},
		{Speaker: "Guest", Text: "First, the ten o'clock review.", Chapter: 0},
		{Pause: 500 * time.Millisecond, Chapter: 0},
		{Speaker: "Guest", Text: "Then lunch.", Chapter: 0},
		{Speaker: "Host", Text: "Two threads need you.", Chapter: 1},
	}
	if len(sc.Segments) != len(want) {
		t.Fatalf("got %d segments: %+v", len(sc.Segments), sc.Segments)
	}
	for i, w := range want {
		if sc.Segments[i] != w {
			t.Errorf("segment %d: got %+v, want %+v", i, sc.Segments[i], w)
		}
	}
}

// A time such as "At 10:30" inside a line is not a speaker label.
func TestTimesAreNotSpeakers(t *testing.T) {
	sc, err := ParseScript("Host: Hello.\nMeet at 10: the usual room.\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Segments) != 1 || sc.Segments[0].Speaker != "Host" {
		t.Errorf("got %+v", sc.Segments)
	}
}

// Intent (spec: say splits long text by paragraph and drops Markdown).
func TestParseTextSplitsParagraphs(t *testing.T) {
	sc, err := ParseText("# Release notes\n\nWe shipped **two** fixes.\nSee [the notes](https://x.y).\n\n- Faster start\n- Smaller downloads\n")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range sc.Segments {
		texts = append(texts, s.Text)
	}
	want := []string{"Release notes", "We shipped two fixes. See the notes.", "Faster start", "Smaller downloads"}
	if len(texts) != len(want) {
		t.Fatalf("got %q", texts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("paragraph %d: got %q, want %q", i, texts[i], want[i])
		}
	}
	if sc.Title != "Release notes" {
		t.Errorf("title %q", sc.Title)
	}
}

// Intent (spec: deck speaks each slide's speaker notes): Marp directives are
// not narration, a slide without notes stays silent, front matter is not a
// slide.
func TestParseDeck(t *testing.T) {
	src := `---
marp: true
title: Quarterly review
---

# Quarterly review

<!-- _class: lead -->
<!-- Welcome: this is the quarterly review. -->

---

## Revenue

- Up 12%

<!--
Revenue grew twelve percent.

[pause 1s] Most of it came from renewals.
-->

---

## Questions?
`
	d, err := ParseDeck(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Quarterly review" || len(d.Slides) != 3 {
		t.Fatalf("got %+v", d)
	}
	if d.Slides[0].Notes != "Welcome: this is the quarterly review." {
		t.Errorf("slide 1 notes %q", d.Slides[0].Notes)
	}
	if d.Slides[1].Title != "Revenue" || d.Slides[2].Notes != "" {
		t.Errorf("slides %+v", d.Slides)
	}
	segs := NotesScript(d.Slides[1].Notes)
	if len(segs) != 3 || segs[1].Pause != time.Second || segs[2].Text != "Most of it came from renewals." {
		t.Errorf("slide 2 segments %+v", segs)
	}
}

func TestDeckWithoutNotesIsAnError(t *testing.T) {
	if _, err := ParseDeck("# One\n\n---\n\n# Two\n"); err == nil {
		t.Error("a deck with no speaker notes should be refused")
	}
}

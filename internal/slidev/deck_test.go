package slidev

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func sample(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("testdata/sample/slides.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func stepCounts(d Deck) []int {
	var n []int
	for _, s := range d.Slides {
		n = append(n, len(s.Steps))
	}
	return n
}

// Intent (spec item 1: notes split at [click]): each slide's narration has
// one step for the opening words and one per [click], so step k plays when
// Slidev shows click k.
func TestParseSplitsNotesAtClicks(t *testing.T) {
	d, err := Parse(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stepCounts(d), []int{3, 4, 5, 4, 3, 6}; !slices.Equal(got, want) {
		t.Fatalf("steps per slide %v, want %v", got, want)
	}
	s := d.Slides[1]
	if s.Title != "How it works" || s.Steps[0] != "Here is how it works." || !strings.HasPrefix(s.Steps[2], "Every step is spoken") {
		t.Errorf("slide 2: %q %q", s.Title, s.Steps)
	}
	if d.Title != "Narrated Slidev decks" || d.Theme != "default" {
		t.Errorf("headmatter %q %q", d.Title, d.Theme)
	}
}

// Intent (spec item 1: numbered clicks are refused, not misread): a
// [click:3] marker cannot be honoured by one clip per step.
func TestParseRefusesNumberedClicks(t *testing.T) {
	_, err := Parse("# One\n\n<!--\nHello\n[click:3] there\n-->\n")
	if err == nil || !strings.Contains(err.Error(), "[click:3]") {
		t.Fatalf("err %v", err)
	}
}

// Intent (spec item 3: start, cues and clicks go into a copy of the deck):
// rendering writes each slide's timing keys and leaves every other line as
// it was, so the copy is the author's deck plus timing.
func TestRenderWritesCuesAndKeepsEverythingElse(t *testing.T) {
	src := sample(t)
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	cues := map[int]Cues{}
	for i, s := range d.Slides {
		c := Cues{Start: float64(i) * 10}
		for k := 1; k < len(s.Steps); k++ {
			c.Clicks = append(c.Clicks, float64(k)+0.1)
		}
		cues[i] = c
	}
	out := d.Render(cues)
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stepCounts(again), stepCounts(d)) {
		t.Fatalf("re-parsed steps %v", stepCounts(again))
	}
	for i, s := range again.Slides {
		fm := s.frontmatter()
		var start float64
		var got []float64
		_ = json.Unmarshal([]byte(fm["start"]), &start)
		_ = json.Unmarshal([]byte(fm["cues"]), &got)
		if start != cues[i].Start || !slices.Equal(got, cues[i].Clicks) || fm["clicks"] != strconv.Itoa(len(cues[i].Clicks)) {
			t.Errorf("slide %d frontmatter %v", i+1, fm)
		}
		if s.Title != d.Slides[i].Title {
			t.Errorf("slide %d title %q, was %q", i+1, s.Title, d.Slides[i].Title)
		}
	}
	// Removing the timing lines gives back the source, apart from the
	// frontmatter blocks added to slides that had none.
	var kept []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "start: ") && !strings.HasPrefix(l, "cues: [") && !strings.HasPrefix(l, "clicks: ") {
			kept = append(kept, l)
		}
	}
	if got := strings.ReplaceAll(strings.Join(kept, "\n"), "---\n---\n", "---\n"); got != src {
		t.Errorf("lines other than the timing changed:\n%s", got)
	}
	// Rendering a rendered deck replaces its timing instead of adding more.
	cues[0] = Cues{Start: 0, Clicks: []float64{9, 9.5}}
	twice := again.Render(cues)
	if n := strings.Count(twice, "\nstart: "); n != len(d.Slides) {
		t.Errorf("%d start keys after rendering twice, want %d", n, len(d.Slides))
	}
	if !strings.Contains(twice, "cues: [9.000, 9.500]") {
		t.Error("new cues not written")
	}
}

// Intent (spec item 3, from the branch review): an author's frontmatter
// survives the timing keys, even a value holding three dashes.
func TestRenderKeepsFrontmatterValuesWithDashes(t *testing.T) {
	src := "---\ntitle: \"Before --- after\"\nlayout: center\n---\n\n# One\n\n<!--\nHello.\n[click] There.\n-->\n"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(d.Render(map[int]Cues{0: {Start: 0, Clicks: []float64{1.5}}}))
	if err != nil {
		t.Fatal(err)
	}
	fm := out.Slides[0].frontmatter()
	if fm["title"] != "Before --- after" || fm["layout"] != "center" || fm["cues"] != "[1.500]" {
		t.Errorf("frontmatter %v", fm)
	}
}

// Intent (spec item 1, from the branch review): a hidden slide is not
// spoken, so its notes cannot block the deck.
func TestHiddenSlideNotesAreNotRead(t *testing.T) {
	d, err := Parse("# One\n\n<!-- Hello. -->\n\n---\nhide: true\n---\n\n# Two\n\n<!--\nOld.\n[click:3] notes\n-->\n")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Slides[1].Hidden || d.Slides[1].Steps != nil {
		t.Errorf("hidden slide %+v", d.Slides[1])
	}
}

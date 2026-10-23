package output

import "testing"

func TestEncodeTOON(t *testing.T) {
	doc := Obj{}.
		Set("engine", "ane").
		Set("port", 8080).
		Set("ratio", 0.5).
		Set("ok", true).
		Set("nothing", nil).
		Set("tags", []string{"a", "b,c"}).
		Set("none", []string{}).
		Set("models", []Obj{
			Obj{}.Set("name", "2026-10-22-day-ahead").Set("state", "render").Set("gb", 13.6),
			Obj{}.Set("name", "other").Set("state", "passed").Set("gb", 12),
		}).
		Set("empty", []Obj{}).
		Set("nested", Obj{}.Set("a", "x: y").Set("b", "-lead"))

	want := `engine: ane
port: 8080
ratio: 0.5
ok: true
nothing: null
tags[2]: a,"b,c"
none[0]:
models[2]{name,state,gb}:
  2026-10-22-day-ahead,render,13.6
  other,passed,12
empty[0]:
nested:
  a: "x: y"
  b: "-lead"
`
	if got := EncodeTOON(doc); got != want {
		t.Errorf("EncodeTOON mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTOONQuotesAmbiguousStrings(t *testing.T) {
	cases := map[string]string{
		"plain":      "plain",
		"":           `""`,
		"true":       `"true"`,
		"42":         `"42"`,
		"-1.5":       `"-1.5"`,
		" lead":      `" lead"`,
		"a\nb":       `"a\nb"`,
		`say "hi"`:   `"say \"hi\""`,
		"path/to/it": "path/to/it",
	}
	for in, want := range cases {
		if got := toonScalar(in); got != want {
			t.Errorf("toonScalar(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestTOONNonUniformRowsUseListForm(t *testing.T) {
	doc := Obj{}.Set("rows", []Obj{
		Obj{}.Set("a", 1).Set("b", 2),
		Obj{}.Set("a", 3),
	})
	want := "rows[2]:\n  - a: 1\n    b: 2\n  - a: 3\n"
	if got := EncodeTOON(doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

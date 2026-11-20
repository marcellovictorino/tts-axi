package slidev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func project(t *testing.T, slides string) (Project, string) {
	t.Helper()
	dir := t.TempDir()
	audio := filepath.Join(dir, "deck.mp3")
	if err := os.WriteFile(audio, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "deck")
	if err := os.MkdirAll(filepath.Join(src, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Project{SourceDir: src, Slides: slides, Audio: audio, Total: 3}, dir
}

// Intent (branch review): what the slides name by a ./ path resolves in the
// narrated copy as it does in the source, a local theme included.
func TestWriteCopiesWhatTheSlidesReferTo(t *testing.T) {
	p, dir := project(t, "---\ntheme: ./my-theme\n---\n\n![diagram](./assets/diagram.png)\n")
	for _, f := range []string{"assets/diagram.png", "my-theme/package.json"} {
		path := filepath.Join(p.SourceDir, f)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "out")
	if err := p.Write(out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"assets/diagram.png", "my-theme/package.json", "public/deck.mp3", "global-top.vue"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}

// Intent (security review): a link in the deck folder that points at a
// file elsewhere on the Mac never carries that file into the episode.
func TestWriteRefusesLinksOutOfTheDeck(t *testing.T) {
	p, dir := project(t, "# One\n")
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.SourceDir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(p.SourceDir, "public", "notes.txt")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	err := p.Write(out)
	if err == nil || !strings.Contains(err.Error(), "links outside the deck folder") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "public", "notes.txt")); err == nil {
		t.Error("the linked file was copied")
	}
	// A link inside the deck is fine.
	_ = os.Remove(filepath.Join(p.SourceDir, "public", "notes.txt"))
	if err := os.WriteFile(filepath.Join(p.SourceDir, "assets", "a.png"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(p.SourceDir, "assets", "a.png"), filepath.Join(p.SourceDir, "public", "a.png")); err != nil {
		t.Fatal(err)
	}
	if err := p.Write(out); err != nil {
		t.Fatalf("a link inside the deck was refused: %v", err)
	}
}

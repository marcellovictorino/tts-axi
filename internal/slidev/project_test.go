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

// Intent (branch reviews): what the slides name by a ./ path resolves in
// the narrated copy as it does in the source, a local theme included, and
// nothing beside it, nor a path only a code example or the notes mention,
// is carried into the episode.
func TestWriteCopiesWhatTheSlidesReferTo(t *testing.T) {
	p, dir := project(t, "---\ntheme: ./my-theme\n---\n\n![diagram](<./assets/diagram.png>)\n\n<img src=\"./assets/icon.svg#detail\">\n\n<script src=\"./assets/node_modules/x.js\"></script>\n\n<!-- see ./drafts/x.png -->\n\nRun `source ./config/.env first`.\n\n```bash\ncp -r ./config ./backup\n```\n")
	for _, f := range []string{"assets/diagram.png", "assets/icon.svg", "assets/private.key", "assets/node_modules/x.js", "my-theme/package.json", "public/deck.mp3", "drafts/x.png", "config/.env"} {
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
	for _, f := range []string{"assets/diagram.png", "assets/icon.svg", "my-theme/package.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	// The deck's own public/deck.mp3 keeps its URL beside the narration.
	if b, _ := os.ReadFile(filepath.Join(out, "public", "deck.mp3")); string(b) != "public/deck.mp3" {
		t.Errorf("the deck's public/deck.mp3 was replaced with %q", b)
	}
	// Only what is named: the diagram's neighbour stays behind.
	// Nor what only a code example or the notes mention.
	for _, f := range []string{"assets/private.key", "assets/node_modules/x.js", "drafts/x.png", "config"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s was copied", f)
		}
	}
	// A deck holding a file the narration would replace is refused.
	_ = os.WriteFile(filepath.Join(p.SourceDir, "public", "tts-axi-narration.mp3"), []byte("x"), 0o644)
	if err := p.Write(filepath.Join(dir, "out2")); err == nil || !strings.Contains(err.Error(), "public/tts-axi-narration.mp3") {
		t.Errorf("err %v", err)
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
	// A folder the slides name through a link out of the deck is refused too.
	_ = os.Remove(filepath.Join(p.SourceDir, "public", "a.png"))
	if err := os.Symlink(dir, filepath.Join(p.SourceDir, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	p.Slides = "![](./elsewhere/secret.txt)\n"
	if err := p.Write(filepath.Join(dir, "out2")); err == nil || !strings.Contains(err.Error(), "links outside the deck folder") {
		t.Fatalf("a file reached through a linked folder: err %v", err)
	}
}

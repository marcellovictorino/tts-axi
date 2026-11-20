package slidev

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// layer is the narration layer written into every narrated deck: the player
// (global-top.vue, which also holds the marks, spotlight and bar styles), the
// laser (slide-top.vue), and the CountUp and PiTerminal components. Slidev
// picks these files up by name. //go:embed compiles the folder into the
// binary.
//
//go:embed layer
var layer embed.FS

// Recorder is the Node script that records a deck; see record.mjs.
//
//go:embed record.mjs
var Recorder []byte

// projectEntries are the files and folders of a Slidev project that a deck
// can rely on; they are copied beside the narrated slides.md. Anything else
// next to the deck (node_modules, dist, other files) is left behind.
var projectEntries = []string{
	"components", "layouts", "public", "styles", "setup", "snippets", "pages",
	"style.css", "global-bottom.vue", "custom-nav-controls.vue",
	"vite.config.ts", "vite.config.js", "uno.config.ts", "unocss.config.ts",
}

// Project is what a narrated deck folder needs.
type Project struct {
	SourceDir string  // the folder holding the original slides.md
	Slides    string  // the narrated slides.md
	Audio     string  // the deck's audio file, copied into public/
	Total     float64 // seconds of narration
	Package   []byte  // package.json pinning Slidev
	Lock      []byte  // its lockfile
}

// LayerConflicts lists the layer files the source deck already has; tts-axi
// will not overwrite a deck's own player, laser or components.
func LayerConflicts(sourceDir string) []string {
	var out []string
	_ = fs.WalkDir(layer, "layer", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "layer/")
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(rel))); err == nil {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// Write builds the narrated deck folder at dir.
func (p Project) Write(dir string) error {
	if c := LayerConflicts(p.SourceDir); len(c) > 0 {
		return fmt.Errorf("the deck already has %s, which tts-axi's narration layer provides", strings.Join(c, ", "))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range projectEntries {
		src := filepath.Join(p.SourceDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyTree(src, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	audioName := "deck" + filepath.Ext(p.Audio)
	err := fs.WalkDir(layer, "layer", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := layer.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "global-top.vue") {
			s := strings.ReplaceAll(string(data), "__TTS_AXI_TOTAL__", strconv.FormatFloat(p.Total, 'f', 3, 64))
			data = []byte(strings.ReplaceAll(s, "__TTS_AXI_AUDIO__", audioName))
		}
		dest := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, "layer/")))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		return err
	}
	files := map[string][]byte{"slides.md": []byte(p.Slides), "package.json": p.Package, "package-lock.json": p.Lock}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return copyFile(p.Audio, filepath.Join(dir, "public", audioName))
}

// copyTree copies a file or folder; links are copied as the files they
// point to.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

package testutil

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
)

// The Slidev route's stand-ins, also run by test-binary re-exec: npm
// "installs" Slidev by writing the files that mark it installed, and node
// running record.mjs acts as the recorder, writing timestamped frames for
// the narration's length the way the real one does.

// FakeRecordFPS is how many frames a second the fake recorder writes.
const FakeRecordFPS = 10

func fakeNpm() int {
	if !slices.Contains(os.Args, "ci") {
		return 1
	}
	lock, err := os.ReadFile("package-lock.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "npm ci: no package-lock.json")
		return 1
	}
	bin := filepath.Join("node_modules", "@slidev", "cli", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return 1
	}
	_ = os.WriteFile(filepath.Join(bin, "slidev.mjs"), []byte("// stand-in\n"), 0o644)
	_ = os.WriteFile(filepath.Join("node_modules", ".package-lock.json"), lock, 0o644)
	return 0
}

// fakeRecord takes record.mjs's arguments: deck folder, frames folder,
// chrome, slidev.mjs, port, seconds.
func fakeRecord() int {
	if len(os.Args) != 8 {
		fmt.Println(`{"error":"fake recorder: wrong arguments"}`)
		return 1
	}
	deck, frames, seconds := os.Args[2], os.Args[3], os.Args[7]
	if _, err := os.Stat(filepath.Join(deck, "slides.md")); err != nil {
		fmt.Println(`{"error":"fake recorder: no slides.md in the deck folder"}`)
		return 1
	}
	secs, err := strconv.ParseFloat(seconds, 64)
	if err != nil {
		fmt.Println(`{"error":"fake recorder: bad seconds"}`)
		return 1
	}
	start := float64(time.Now().UnixMilli()) / 1000
	n := int((secs+1)*FakeRecordFPS) + 1
	var stamps []float64
	for i := range n {
		img := image.NewRGBA(image.Rect(0, 0, 320, 180))
		c := color.RGBA{uint8(i * 7), 40, 90, 255}
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = c.R, c.G, c.B, c.A
		}
		var b bytes.Buffer
		_ = jpeg.Encode(&b, img, nil)
		if err := os.WriteFile(filepath.Join(frames, fmt.Sprintf("f%05d.jpg", i)), b.Bytes(), 0o644); err != nil {
			return 1
		}
		// The first frame was on screen before narration started.
		stamps = append(stamps, start-0.2+float64(i)/FakeRecordFPS)
	}
	data, _ := json.Marshal(map[string]any{"start": start, "frames": stamps})
	if err := os.WriteFile(filepath.Join(frames, "frames.json"), data, 0o644); err != nil {
		return 1
	}
	fmt.Printf(`{"frames":%d,"end":"/1"}`+"\n", n)
	return 0
}

// ChromeSource serves a stand-in Chrome for Testing archive, a zip holding
// only the executable's path, pinned the way the real one is.
func ChromeSource(t *testing.T) engine.ChromeSource {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing")
	must(t, err)
	_, _ = w.Write([]byte("stand-in"))
	must(t, zw.Close())
	body := b.Bytes()
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chrome-mac-arm64.zip") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return engine.ChromeSource{BaseURL: srv.URL, Zip: engine.Pin{Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
}

// InstallSlidev marks Slidev and Chrome for Testing installed under home,
// as doctor --fix leaves them, with stand-in files.
func InstallSlidev(t *testing.T, home string) {
	t.Helper()
	e := engine.Engine{Paths: config.Paths{Home: home}}
	must(t, os.MkdirAll(filepath.Dir(e.SlidevBin()), 0o755))
	must(t, os.WriteFile(e.SlidevBin(), []byte("// stand-in\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(e.SlidevDir(), "package-lock.json"), engine.SlidevLock, 0o644))
	must(t, os.WriteFile(filepath.Join(e.SlidevDir(), "node_modules", ".package-lock.json"), engine.SlidevLock, 0o644))
	must(t, os.MkdirAll(filepath.Dir(e.ChromeBin()), 0o755))
	must(t, os.WriteFile(e.ChromeBin(), []byte("stand-in"), 0o755))
}

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/marcellovictorino/tts-axi/internal/config"
)

// Slidev narration (`tts-axi deck --slidev --video`) needs two more pieces,
// both installed into tts-axi's own folder by `tts-axi doctor --fix`:
//
//   - Slidev, the Markdown slide tool, with its themes and playwright-core
//     (the browser driver the recorder uses). npm installs exactly the
//     packages the embedded lockfile names, each checked against the
//     lockfile's SHA-512 integrity; the lockfile itself is pinned below.
//     Install scripts are not run.
//   - Chrome for Testing, Google's build of Chrome for automation, pinned by
//     size and SHA-256 like Gradium's release assets. Its version is the one
//     playwright-core was released against.

// SlidevPackage and SlidevLock are the npm project doctor --fix installs.
// The episode's slidev folder gets the same two files, so `npm ci` there
// installs the same Slidev.
var (
	//go:embed slidev/package.json
	SlidevPackage []byte
	//go:embed slidev/package-lock.json
	SlidevLock []byte
)

// SlidevVersion is the pinned @slidev/cli release.
const SlidevVersion = "53.1.0"

// slidevLockPin pins the embedded lockfile: a changed lockfile must update
// this pin too, as a new runtime updates its asset pins.
var slidevLockPin = Pin{Size: 349972, SHA256: "2e5de512b9756229a6ccfd4b2a57a2896310fe6dfea84af0bc15fee440798777"}

// ChromeVersion is the Chrome for Testing build playwright-core 1.65.1 was
// released against.
const ChromeVersion = "159.0.8154.6"

var chromeAsset = asset{"chrome-mac-arm64.zip", 201252789, "43609676a41e29d1c7667935eb914a422eca98b2e49dcbfd6f3e9794202de12a"}

// ChromeSource is where Chrome for Testing comes from. The zero value is
// Google's storage and the pinned archive; tests serve their own.
type ChromeSource struct {
	BaseURL string // serves BaseURL/chrome-mac-arm64.zip
	Zip     Pin
}

func (e *Engine) chromeSource() (string, asset) {
	if e.Chrome.BaseURL != "" {
		return e.Chrome.BaseURL + "/" + chromeAsset.Name, asset{chromeAsset.Name, e.Chrome.Zip.Size, e.Chrome.Zip.SHA256}
	}
	return "https://storage.googleapis.com/chrome-for-testing-public/" + ChromeVersion + "/mac-arm64/" + chromeAsset.Name, chromeAsset
}

func (e *Engine) node() string { return orDefault(e.Node, "node") }
func (e *Engine) npm() string  { return orDefault(e.Npm, "npm") }

// NodeBin is the node that runs Slidev and the recorder.
func (e *Engine) NodeBin() string { return e.node() }

// SlidevDir is the npm project Slidev is installed in. Decks are recorded
// from folders inside it, so Node finds its node_modules by walking up.
func (e *Engine) SlidevDir() string { return filepath.Join(e.Paths.Home, "slidev", SlidevVersion) }

// SlidevBin is Slidev's command-line entry point.
func (e *Engine) SlidevBin() string {
	return filepath.Join(e.SlidevDir(), "node_modules", "@slidev", "cli", "bin", "slidev.mjs")
}

// SlidevInstalled reports whether npm finished installing the pinned
// lockfile: the lockfile npm keeps in node_modules is written last.
func (e *Engine) SlidevInstalled() bool {
	lock, err := os.ReadFile(filepath.Join(e.SlidevDir(), "package-lock.json"))
	if err != nil || !bytes.Equal(lock, SlidevLock) {
		return false
	}
	if _, err := os.Stat(filepath.Join(e.SlidevDir(), "node_modules", ".package-lock.json")); err != nil {
		return false
	}
	_, err = os.Stat(e.SlidevBin())
	return err == nil
}

// InstallSlidev writes the pinned npm project and runs `npm ci` in it
// (about 560 MB, with install scripts off). It needs Node.js.
func (e *Engine) InstallSlidev(ctx context.Context) error {
	if e.SlidevInstalled() {
		return nil
	}
	sum := sha256.Sum256(SlidevLock)
	if int64(len(SlidevLock)) != slidevLockPin.Size || hex.EncodeToString(sum[:]) != slidevLockPin.SHA256 {
		return fmt.Errorf("the embedded Slidev lockfile does not match its pin")
	}
	dir := e.SlidevDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(filepath.Join(dir, "package.json"), SlidevPackage, 0o644); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(filepath.Join(dir, "package-lock.json"), SlidevLock, 0o644); err != nil {
		return err
	}
	if e.Progress != nil {
		e.Progress("installing Slidev %s with npm (about 560 MB)", SlidevVersion)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.npm(), "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error")
	cmd.Dir = dir
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm ci failed: %s", firstLines(stderr.String(), 3))
	}
	if !e.SlidevInstalled() {
		return fmt.Errorf("npm ci finished but Slidev is not in %s", dir)
	}
	return nil
}

// ChromeDir holds the unpacked Chrome for Testing.
func (e *Engine) ChromeDir() string { return filepath.Join(e.Paths.Home, "chrome", ChromeVersion) }

// ChromeBin is the Chrome for Testing executable.
func (e *Engine) ChromeBin() string {
	return filepath.Join(e.ChromeDir(), "chrome-mac-arm64", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
}

// ChromeInstalled reports whether Chrome for Testing is unpacked.
func (e *Engine) ChromeInstalled() bool {
	_, err := os.Stat(e.ChromeBin())
	return err == nil
}

// InstallChrome downloads Chrome for Testing (201 MB), checks it against its
// pinned size and SHA-256, and unpacks it with ditto, which keeps the app's
// links and signature intact (Go's zip reader would drop the links).
func (e *Engine) InstallChrome(ctx context.Context) error {
	if e.ChromeInstalled() {
		return nil
	}
	tmp := e.ChromeDir() + ".download"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	url, a := e.chromeSource()
	zipPath := filepath.Join(tmp, a.Name)
	if e.Progress != nil {
		e.Progress("downloading Chrome for Testing %s (%.0f MB)", ChromeVersion, float64(a.Size)/1e6)
	}
	if err := e.download(ctx, url, zipPath, a, ""); err != nil {
		return fmt.Errorf("downloading Chrome for Testing: %w", err)
	}
	out := filepath.Join(tmp, "unpacked")
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ditto", "-x", "-k", zipPath, out)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unpacking Chrome for Testing: %s", firstLines(stderr.String(), 3))
	}
	if _, err := os.Stat(filepath.Join(out, "chrome-mac-arm64")); err != nil {
		return fmt.Errorf("the Chrome for Testing archive has no chrome-mac-arm64 folder")
	}
	_ = os.RemoveAll(e.ChromeDir())
	return os.Rename(out, e.ChromeDir())
}

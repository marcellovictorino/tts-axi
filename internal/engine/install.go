package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcellovictorino/tts-axi/internal/config"
)

// MissingError says a piece of the installation is absent; `tts-axi doctor
// --fix` installs it.
type MissingError struct{ What string }

func (e *MissingError) Error() string { return e.What }

func errMissing(what string) error { return &MissingError{What: what} }

// IsMissing reports whether err is a MissingError.
func IsMissing(err error) bool {
	var m *MissingError
	return errors.As(err, &m)
}

func (e *Engine) runtimeDir() string { return filepath.Join(e.Paths.RuntimeDir(), Release) }

func (e *Engine) frameworkDir() string {
	return filepath.Join(e.runtimeDir(), "PhononCore.xcframework", "macos-arm64")
}

func (e *Engine) wrapperSource() string {
	return filepath.Join(e.runtimeDir(), "ptts-swift-0.4.0", "Sources", "PhononTTS", "Phonon.swift")
}

// RuntimeInstalled reports whether Gradium's Swift runtime is unpacked.
func (e *Engine) RuntimeInstalled() bool {
	_, err1 := os.Stat(filepath.Join(e.frameworkDir(), "libptts_ffi.a"))
	_, err2 := os.Stat(e.wrapperSource())
	return err1 == nil && err2 == nil
}

// InstallRuntime downloads and unpacks the released framework and Swift
// wrapper (about 39 MB), checking each against its pinned size and SHA-256.
func (e *Engine) InstallRuntime(ctx context.Context) error {
	// The runtime is only needed to build the helper.
	if e.RuntimeInstalled() || e.HelperCurrent() {
		return nil
	}
	for _, a := range []asset{frameworkAsset, wrapperAsset} {
		if err := e.fetchAndUnzip(ctx, a, e.runtimeDir()); err != nil {
			return err
		}
	}
	return nil
}

// HelperCurrent reports whether the built helper matches this tts-axi's
// embedded source.
func (e *Engine) HelperCurrent() bool {
	src, err := os.ReadFile(filepath.Join(filepath.Dir(e.Paths.HelperBin()), "main.swift"))
	if err != nil || !bytes.Equal(src, SwiftHelper) {
		return false
	}
	_, err = os.Stat(e.Paths.HelperBin())
	return err == nil
}

// BuildHelper compiles the Swift helper with swiftc against the runtime.
func (e *Engine) BuildHelper(ctx context.Context) error {
	if e.HelperCurrent() {
		return nil
	}
	if !e.RuntimeInstalled() {
		return errMissing("the Phonon runtime is not installed")
	}
	dir := filepath.Dir(e.Paths.HelperBin())
	build := filepath.Join(dir, "build")
	if err := os.MkdirAll(filepath.Join(dir, "module-cache"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		return err
	}
	// swiftc needs the .swift extension, so the new source is staged in
	// build/ and moved beside the binary only after a successful build.
	src := filepath.Join(build, "main.swift")
	tmpBin := filepath.Join(build, "tts-axi-helper")
	if err := os.WriteFile(src, SwiftHelper, 0o644); err != nil {
		return err
	}
	fw := e.frameworkDir()
	args := []string{"-O", "-parse-as-library",
		"-module-cache-path", filepath.Join(dir, "module-cache"),
		"-I", filepath.Join(fw, "Headers"),
		src, e.wrapperSource(), filepath.Join(fw, "libptts_ffi.a"),
		// The frameworks PhononCore links against, as its Package.swift lists them.
		"-framework", "CoreML", "-framework", "CoreVideo", "-framework", "Accelerate", "-framework", "AVFoundation",
		"-o", tmpBin}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.swiftc(), args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building the Swift helper failed: %s", firstLines(stderr.String(), 3))
	}
	if err := os.Rename(tmpBin, e.Paths.HelperBin()); err != nil {
		return err
	}
	// The source is saved last: it marks the binary as built from it.
	return os.Rename(src, filepath.Join(dir, "main.swift"))
}

// BundleInstalled reports whether the English Core ML bundle is present.
func (e *Engine) BundleInstalled() bool {
	_, err := os.Stat(filepath.Join(e.Paths.ModelsDir(), BundleName, "bundle.json"))
	return err == nil
}

// InstallBundle downloads the English Core ML bundle (137 MB), checks the zip
// against its pinned SHA-256 and every file against bundle.json.
func (e *Engine) InstallBundle(ctx context.Context) error {
	if e.BundleInstalled() {
		return nil
	}
	tmp := filepath.Join(e.Paths.ModelsDir(), ".download")
	_ = os.RemoveAll(tmp)
	if err := e.fetchAndUnzip(ctx, bundleAsset, tmp); err != nil {
		return err
	}
	dir := filepath.Join(tmp, BundleName)
	if err := VerifyBundle(dir); err != nil {
		return err
	}
	if err := os.Rename(dir, filepath.Join(e.Paths.ModelsDir(), BundleName)); err != nil {
		return err
	}
	return os.RemoveAll(tmp)
}

// VerifyBundle checks every file bundle.json lists against its size and SHA-256.
func VerifyBundle(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return err
	}
	var b struct {
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("bundle.json: %w", err)
	}
	if len(b.Files) == 0 {
		return fmt.Errorf("bundle.json lists no files")
	}
	for _, f := range b.Files {
		sum, size, err := hashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		if size != f.Size || sum != f.SHA256 {
			return fmt.Errorf("model file %s does not match bundle.json", f.Path)
		}
	}
	return nil
}

// WritePttsHelper writes the multilingual helper script if it changed.
func (e *Engine) WritePttsHelper() (string, error) {
	path := filepath.Join(e.Paths.PttsDir(), "helper.py")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, PttsHelper) {
		return path, nil
	}
	if err := os.MkdirAll(e.Paths.PttsDir(), 0o755); err != nil {
		return "", err
	}
	return path, config.WriteFileAtomic(path, PttsHelper, 0o644)
}

// PttsArgs is the uv command line that runs the multilingual helper in a
// cached, isolated environment with the pinned ptts.
func (e *Engine) PttsArgs(script string) []string {
	return []string{"run", "--quiet", "--no-project", "--python", "3.12",
		"--with", "ptts==" + PttsVersion, "python", "-I", script}
}

// hfHubCache is the Hugging Face cache folder, as huggingface_hub and ptts
// resolve it.
func hfHubCache() string {
	if v := os.Getenv("HF_HUB_CACHE"); v != "" {
		return v
	}
	if v := os.Getenv("HF_HOME"); v != "" {
		return filepath.Join(v, "hub")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "huggingface", "hub")
}

// MultilingualCached reports whether the pinned multilingual model is in the
// Hugging Face cache, so French and Portuguese work with no sign-in.
func MultilingualCached() bool {
	snap := filepath.Join(hfHubCache(), "models--"+strings.ReplaceAll(MultilingualRepo, "/", "--"), "snapshots", MultilingualRevision)
	for _, f := range multilingualFiles {
		if _, err := os.Stat(filepath.Join(snap, f)); err != nil {
			return false
		}
	}
	return true
}

// HFSignedIn reports whether a Hugging Face token is available: HF_TOKEN in
// the environment, or the file `hf auth login` writes. The token is never read.
func HFSignedIn() bool {
	if os.Getenv("HF_TOKEN") != "" {
		return true
	}
	home := os.Getenv("HF_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".cache", "huggingface")
	}
	_, err := os.Stat(filepath.Join(home, "token"))
	return err == nil
}

// InstallMultilingual downloads the pinned multilingual model (about 75 MB)
// into the Hugging Face cache with the hf command, using whatever token the
// environment holds. It loads no model.
func (e *Engine) InstallMultilingual(ctx context.Context) error {
	if MultilingualCached() {
		return nil
	}
	args := append([]string{"tool", "run", "--quiet", "--from", "huggingface_hub", "hf", "download", MultilingualRepo},
		multilingualFiles...)
	args = append(args, "--revision", MultilingualRevision)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.uv(), args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := firstLines(stderr.String(), 3)
		if strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(strings.ToLower(msg), "gated") {
			msg = "Hugging Face refused the download: accept the model's terms at huggingface.co/" + MultilingualRepo + " with the account the token belongs to (" + msg + ")"
		}
		return fmt.Errorf("downloading the French and Portuguese model failed: %s", msg)
	}
	if !MultilingualCached() {
		return fmt.Errorf("the French and Portuguese model download finished, but its files are not in %s", hfHubCache())
	}
	return nil
}

// WarmPtts makes uv fetch ptts now, so the first French or Portuguese render
// does not wait for it. No model is loaded.
func (e *Engine) WarmPtts(ctx context.Context) error {
	args := []string{"run", "--quiet", "--no-project", "--python", "3.12",
		"--with", "ptts==" + PttsVersion, "python", "-I", "-c", "import ptts"}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.uv(), args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing ptts with uv failed: %s", firstLines(stderr.String(), 3))
	}
	return nil
}

func (e *Engine) fetchAndUnzip(ctx context.Context, a asset, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	zipPath := filepath.Join(dest, a.Name+".part")
	defer func() { _ = os.Remove(zipPath) }()
	if e.Progress != nil {
		e.Progress("downloading %s (%.0f MB)", a.Name, float64(a.Size)/1e6)
	}
	if err := e.download(ctx, e.releaseURL(a.Name), zipPath, a); err != nil {
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	return unzip(zipPath, dest)
}

func (e *Engine) releaseURL(name string) string {
	if e.ReleaseBase != "" {
		return e.ReleaseBase + "/" + name
	}
	return releaseURL(name)
}

func (e *Engine) download(ctx context.Context, url, dest string, a asset) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	// Read one byte past the expected size, so an oversized file is caught
	// without downloading all of it.
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != a.Size {
		return fmt.Errorf("got %d bytes, expected %d", n, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("checksum mismatch: got %s", got)
	}
	return nil
}

// unzip extracts an archive under dest, refusing entries that would land
// outside it.
func unzip(path, dest string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, f := range r.File {
		target := filepath.Join(root, filepath.FromSlash(f.Name))
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry %q escapes the destination", f.Name)
		}
		if strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive entry %q is a link, which is not expected", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}

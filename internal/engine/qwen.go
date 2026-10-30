package engine

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/marcellovictorino/tts-axi/internal/config"
)

// QwenHelper is the cloned-voice helper: Qwen3-TTS Base through mlx-audio, run
// by uv in a throwaway environment like the multilingual helper.
//
//go:embed qwen/helper.py
var QwenHelper []byte

// The cloned-voice model is mlx-community/Qwen3-TTS-12Hz-1.7B-Base-8bit
// (Apache 2.0, about 3 GB), fetched from Hugging Face at one pinned revision.
// The repository is public, so no account or token is needed. Every file is
// pinned by size and SHA-256.
const (
	QwenName     = "qwen3-tts-12hz-1.7b-base-8bit" // folder under ~/.tts-axi/models
	QwenRepo     = "mlx-community/Qwen3-TTS-12Hz-1.7B-Base-8bit"
	QwenRevision = "e7dd0585652209fa0d7783659aad4e8a324de11c"
	// MlxAudioVersion is the mlx-audio release uv installs for the helper.
	MlxAudioVersion = "0.6.3"
	qwenHost        = "https://huggingface.co"
	qwenPinFile     = ".pinned" // written last, after every file verified
)

// QwenFile is one model file with its pin; Path is relative to the model folder.
type QwenFile struct {
	Path   string
	Size   int64
	SHA256 string
}

// QwenSource is where the cloned-voice model comes from. The zero value is
// Hugging Face and the pinned files; tests serve their own.
type QwenSource struct {
	BaseURL string // serves BaseURL/<Path>
	Files   []QwenFile
}

var qwenFiles = []QwenFile{
	{"config.json", 5522, "b14fba6bfd391968876248b843b095696b0188187a3d774a3bddc32544d15627"},
	{"generation_config.json", 245, "f1b90b4513f3b34c62851049e2492d7b4c5940daf1276f89c82b8ef04127f3aa"},
	{"merges.txt", 1671839, "599bab54075088774b1733fde865d5bd747cbcc7a547c5bc12610e874e26f5e3"},
	{"model.safetensors", 2417320525, "b965c581ccf6aa852a4124feeb7a8a111542ee7b213139368b4cc7ba7fd4728b"},
	{"model.safetensors.index.json", 78070, "9592a7c0ac3261a6211978416ef1796d3841225ac0ff0f9d6bfb5d1a6b49e5e9"},
	{"preprocessor_config.json", 127, "efdde1022ea9d76928bf7a9cd53139138f5ba2e466e837f08f6105ab1af1c119"},
	{"speech_tokenizer/config.json", 2336, "ee65bb901c876664ab8707c487157aa1a6ee57c65969b28fb5ec9dc211e68167"},
	{"speech_tokenizer/configuration.json", 76, "6bc26d64eb5024b4d1dab5a52371958b429256d6c9d59787f1f5294a54e0cebd"},
	{"speech_tokenizer/model.safetensors", 682293092, "836b7b357f5ea43e889936a3709af68dfe3751881acefe4ecf0dbd30ba571258"},
	{"speech_tokenizer/preprocessor_config.json", 234, "fcb3805e597e786d4067706e602f6688524640f8d3396790e2e09b5942fcbdfb"},
	{"tokenizer_config.json", 7344, "dc3c31c3bdaedd5016382bb3cbe07323026775ad51f5a4fb564505992ae4a670"},
	{"vocab.json", 2776833, "ca10d7e9fb3ed18575dd1e277a2579c16d108e32f27439684afa0e10b1440910"},
}

func (e *Engine) qwenSource() QwenSource {
	if e.Qwen.BaseURL != "" {
		return e.Qwen
	}
	return QwenSource{BaseURL: qwenHost + "/" + QwenRepo + "/resolve/" + QwenRevision, Files: qwenFiles}
}

// QwenDir is the local model folder the helper loads.
func (e *Engine) QwenDir() string { return filepath.Join(e.Paths.ModelsDir(), QwenName) }

// QwenInstalled reports whether every file is in place at its pinned size and
// the folder was marked complete by a verified download.
func (e *Engine) QwenInstalled() bool {
	dir := e.QwenDir()
	for _, f := range e.qwenSource().Files {
		fi, err := os.Stat(filepath.Join(dir, f.Path))
		if err != nil || fi.Size() != f.Size {
			return false
		}
	}
	_, err := os.Stat(filepath.Join(dir, qwenPinFile))
	return err == nil
}

// InstallQwen downloads the model files that are missing or the wrong size.
// A file is checked against its SHA-256 as it downloads, so a file that is
// present at the right size was verified when it arrived.
func (e *Engine) InstallQwen(ctx context.Context) error {
	if e.QwenInstalled() {
		return nil
	}
	src := e.qwenSource()
	dir := e.QwenDir()
	_ = os.Remove(filepath.Join(dir, qwenPinFile))
	var total int64
	for _, f := range src.Files {
		total += f.Size
	}
	e.progress("downloading the cloned-voice model (%.1f GB)", float64(total)/1e9)
	for _, f := range src.Files {
		dest := filepath.Join(dir, f.Path)
		if fi, err := os.Stat(dest); err == nil && fi.Size() == f.Size {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		part := dest + ".part"
		if err := e.download(ctx, src.BaseURL+"/"+f.Path, part, asset{f.Path, f.Size, f.SHA256}, ""); err != nil {
			_ = os.Remove(part)
			return fmt.Errorf("downloading %s: %w", f.Path, err)
		}
		if err := os.Rename(part, dest); err != nil {
			return err
		}
	}
	return config.WriteFileAtomic(filepath.Join(dir, qwenPinFile), []byte(QwenRevision+"\n"), 0o644)
}

// WriteQwenHelper writes the cloned-voice helper script if it changed.
func (e *Engine) WriteQwenHelper() (string, error) {
	path := filepath.Join(e.Paths.QwenHelperDir(), "helper.py")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, QwenHelper) {
		return path, nil
	}
	if err := os.MkdirAll(e.Paths.QwenHelperDir(), 0o755); err != nil {
		return "", err
	}
	return path, config.WriteFileAtomic(path, QwenHelper, 0o644)
}

// QwenArgs is the uv command line that runs the cloned-voice helper in a
// cached, isolated environment with the pinned mlx-audio.
func (e *Engine) QwenArgs(script string) []string {
	return []string{"run", "--quiet", "--no-project", "--python", "3.12",
		"--with", "mlx-audio==" + MlxAudioVersion, "python", "-I", script}
}

// WarmQwen makes uv fetch mlx-audio now, so the first cloned render does not
// wait for it. No model is loaded.
func (e *Engine) WarmQwen(ctx context.Context) error {
	args := []string{"run", "--quiet", "--no-project", "--python", "3.12",
		"--with", "mlx-audio==" + MlxAudioVersion, "python", "-I", "-c", "import mlx_audio"}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.uv(), args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing mlx-audio with uv failed: %s", firstLines(stderr.String(), 3))
	}
	return nil
}

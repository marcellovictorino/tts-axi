package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Pin is a download's expected size and SHA-256.
type Pin struct {
	Size   int64
	SHA256 string
}

// Registry is where the multilingual model comes from. The zero value is
// Ollama's registry with the pinned manifest; tests serve their own.
type Registry struct {
	Base     string // the registry's address, without /v2
	Manifest Pin
}

func (e *Engine) registry() Registry {
	if e.Registry.Base != "" {
		return e.Registry
	}
	return Registry{Base: OllamaRegistry, Manifest: multilingualManifest}
}

// manifestAccept asks the registry for the manifest format `ollama pull` reads.
const manifestAccept = "application/vnd.docker.distribution.manifest.v2+json"

// The files ptts reads from a local model folder, and the ones tts-axi adds.
const (
	modelFile        = "model.safetensors"
	defaultVoiceFile = "default-voice.safetensors"
	manifestFile     = "manifest.json" // the pinned manifest the folder was built from
)

var multilingualFiles = []string{"config.json", "tokenizer.json", modelFile, defaultVoiceFile}

// MultilingualDir is the local model folder ptts loads for French and
// Portuguese.
func (e *Engine) MultilingualDir() string {
	return filepath.Join(e.Paths.ModelsDir(), MultilingualName)
}

// MultilingualInstalled reports whether the model folder is complete and was
// built from the pinned manifest.
func (e *Engine) MultilingualInstalled() bool {
	dir := e.MultilingualDir()
	for _, f := range multilingualFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	sum, size, err := hashFile(filepath.Join(dir, manifestFile))
	pin := e.registry().Manifest
	return err == nil && size == pin.Size && sum == pin.SHA256
}

type ollamaLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Name      string `json:"name"`
}

const (
	tensorLayer  = "application/vnd.ollama.image.tensor"
	jsonLayer    = "application/vnd.ollama.image.json"
	licenseLayer = "application/vnd.ollama.image.license"
)

// InstallMultilingual downloads the pinned phonon:40m-multilingual from
// Ollama's registry (about 122 MB, no sign-in) and turns it into a folder ptts
// loads: the tensors merged into model.safetensors, the default voice in
// default-voice.safetensors, config.json, tokenizer.json and the licences.
// Every blob is checked against its digest. It loads no model.
func (e *Engine) InstallMultilingual(ctx context.Context) error {
	if e.MultilingualInstalled() {
		return nil
	}
	reg := e.registry()
	tmp := filepath.Join(e.Paths.ModelsDir(), ".download-"+MultilingualName)
	_ = os.RemoveAll(tmp)
	blobs, out := filepath.Join(tmp, "blobs"), filepath.Join(tmp, "model")
	for _, d := range []string{blobs, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	base := reg.Base + "/v2/" + OllamaRepository
	manifest := filepath.Join(out, manifestFile)
	if err := e.download(ctx, base+"/manifests/"+OllamaTag, manifest, asset{OllamaTag, reg.Manifest.Size, reg.Manifest.SHA256}, manifestAccept); err != nil {
		return fmt.Errorf("downloading the manifest of phonon:%s: %w", OllamaTag, err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var m struct {
		Layers []ollamaLayer `json:"layers"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("reading the manifest: %w", err)
	}

	var layers []ollamaLayer
	var total int64
	for _, l := range m.Layers {
		if wanted(l) {
			if !strings.HasPrefix(l.Digest, "sha256:") {
				return fmt.Errorf("manifest layer %q has digest %q", l.Name, l.Digest)
			}
			layers = append(layers, l)
			total += l.Size
		}
	}
	e.progress("downloading the French and Portuguese model from Ollama's registry (%.0f MB, %d files)", float64(total)/1e6, len(layers))
	blobPath := func(l ollamaLayer) string { return filepath.Join(blobs, strings.TrimPrefix(l.Digest, "sha256:")) }
	if err := e.downloadBlobs(ctx, base, layers, blobPath); err != nil {
		return err
	}

	var tensors []tensorRef
	var voice *tensorRef
	var licences [][]byte
	for _, l := range layers {
		switch l.MediaType {
		case tensorLayer:
			t, err := readTensorRef(blobPath(l))
			if err != nil {
				return fmt.Errorf("tensor %s: %w", l.Name, err)
			}
			if t.name == "voices.default" {
				voice = &t
			} else {
				tensors = append(tensors, t)
			}
		case jsonLayer:
			if err := os.Rename(blobPath(l), filepath.Join(out, l.Name)); err != nil {
				return err
			}
		case licenseLayer:
			b, err := os.ReadFile(blobPath(l))
			if err != nil {
				return err
			}
			licences = append(licences, b)
		}
	}
	if voice == nil {
		return fmt.Errorf("the manifest has no voices.default tensor")
	}
	if err := writeSafetensors(filepath.Join(out, modelFile), tensors, nil); err != nil {
		return err
	}
	// The default voice as Hugging Face publishes it, byte for byte.
	v := *voice
	v.name = "speaker_wavs"
	if err := writeSafetensors(filepath.Join(out, defaultVoiceFile), []tensorRef{v}, [][2]string{{"sig", "ba7a5a45"}, {"epoch", "300"}}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "LICENSE"), bytes.Join(licences, []byte("\n\n")), 0o644); err != nil {
		return err
	}
	for _, f := range multilingualFiles {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			return fmt.Errorf("the manifest has no %s", f)
		}
	}
	// The folder appears whole or not at all.
	dir := e.MultilingualDir()
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(out, dir); err != nil {
		return err
	}
	return os.RemoveAll(tmp)
}

// wanted picks the layers ptts needs: the model's tensors and the default
// voice, but not the other voices, which ptts reads from Gradium's voice
// catalog; config.json and tokenizer.json; and the licences.
func wanted(l ollamaLayer) bool {
	switch l.MediaType {
	case tensorLayer:
		return !strings.HasPrefix(l.Name, "voices.") || l.Name == "voices.default"
	case jsonLayer:
		return l.Name == "config.json" || l.Name == "tokenizer.json"
	}
	return l.MediaType == licenseLayer
}

// downloadBlobs fetches the layers a few at a time; each is checked against
// the size and SHA-256 its digest names.
func (e *Engine) downloadBlobs(ctx context.Context, base string, layers []ollamaLayer, path func(ollamaLayer) string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan ollamaLayer)
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for l := range work {
				a := asset{l.Name, l.Size, strings.TrimPrefix(l.Digest, "sha256:")}
				if err := e.download(ctx, base+"/blobs/"+l.Digest, path(l), a, ""); err != nil {
					once.Do(func() { first = fmt.Errorf("downloading %s: %w", orDefault(l.Name, l.Digest), err); cancel() })
				}
			}
		}()
	}
	for _, l := range layers {
		select {
		case work <- l:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	if first == nil {
		first = ctx.Err()
	}
	return first
}

// tensorInfo is one entry of a safetensors header.
type tensorInfo struct {
	Dtype   string   `json:"dtype"`
	Shape   []int64  `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

// tensorRef is one tensor inside a file, by where its bytes start.
type tensorRef struct {
	name  string
	info  tensorInfo
	path  string
	start int64 // file offset of the tensor's first byte
}

func (t tensorRef) size() int64 { return t.info.Offsets[1] - t.info.Offsets[0] }

// readTensorRef reads the header of a safetensors file that holds one
// tensor, as each of Ollama's tensor layers does. The layout is an 8-byte
// little-endian header length, the JSON header, then the tensor bytes.
func readTensorRef(path string) (tensorRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return tensorRef{}, err
	}
	defer func() { _ = f.Close() }()
	var n uint64
	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return tensorRef{}, err
	}
	if n > 1<<20 {
		return tensorRef{}, fmt.Errorf("header of %d bytes is not expected", n)
	}
	hdr := make([]byte, n)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return tensorRef{}, err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(hdr, &entries); err != nil {
		return tensorRef{}, err
	}
	delete(entries, "__metadata__")
	if len(entries) != 1 {
		return tensorRef{}, fmt.Errorf("holds %d tensors, expected one", len(entries))
	}
	for name, raw := range entries {
		t := tensorRef{name: name, path: path}
		if err := json.Unmarshal(raw, &t.info); err != nil {
			return tensorRef{}, err
		}
		t.start = 8 + int64(n) + t.info.Offsets[0]
		return t, nil
	}
	panic("unreachable")
}

// writeSafetensors writes the tensors, in order, into one safetensors file
// with optional string metadata. The header is written by hand because Go's
// JSON encoder sorts map keys, and the order is part of the file.
func writeSafetensors(path string, tensors []tensorRef, meta [][2]string) error {
	var hdr bytes.Buffer
	hdr.WriteByte('{')
	var off int64
	for i, t := range tensors {
		if i > 0 {
			hdr.WriteByte(',')
		}
		info := t.info
		info.Offsets = [2]int64{off, off + t.size()}
		off += t.size()
		writeJSON(&hdr, t.name)
		hdr.WriteByte(':')
		writeJSON(&hdr, info)
	}
	if len(meta) > 0 {
		hdr.WriteString(`,"__metadata__":{`)
		for i, kv := range meta {
			if i > 0 {
				hdr.WriteByte(',')
			}
			writeJSON(&hdr, kv[0])
			hdr.WriteByte(':')
			writeJSON(&hdr, kv[1])
		}
		hdr.WriteByte('}')
	}
	hdr.WriteByte('}')
	// The tensor data starts on an 8-byte boundary; the header is padded
	// with spaces, as the safetensors library does.
	for hdr.Len()%8 != 0 {
		hdr.WriteByte(' ')
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = writeTensors(f, hdr.Bytes(), tensors)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func writeTensors(w io.Writer, hdr []byte, tensors []tensorRef) error {
	if err := binary.Write(w, binary.LittleEndian, uint64(len(hdr))); err != nil {
		return err
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, t := range tensors {
		src, err := os.Open(t.path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, io.NewSectionReader(src, t.start, t.size()))
		_ = src.Close()
		if err != nil {
			return fmt.Errorf("copying %s: %w", t.name, err)
		}
	}
	return nil
}

func writeJSON(b *bytes.Buffer, v any) {
	data, _ := json.Marshal(v) // strings and tensorInfo always encode
	b.Write(data)
}

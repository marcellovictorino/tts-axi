package engine_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

// hfDefaultVoice is the SHA-256 of default-voice.safetensors in
// gradium/phonon-40m-multilingual at revision 1537ce46c64f42903e0d608b132ccb073dac46d2,
// the revision Ollama's licence layer names as its source (Hugging Face's
// own LFS digest for that file).
const hfDefaultVoice = "dc537478eaadc1a5257fcda2e3501ab81e14c111a5d95a5838f350db2afd0f1a"

// tensorNames lists a safetensors file's tensors in header order.
func tensorNames(t *testing.T, file []byte) []string {
	t.Helper()
	n := binary.LittleEndian.Uint64(file)
	dec := json.NewDecoder(bytes.NewReader(file[8 : 8+n]))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
		if k != "__metadata__" {
			names = append(names, k.(string))
		}
	}
	return names
}

// Intent (owner request: get the Portuguese and French weights by the Ollama
// pull route, without the Ollama engine and without a Hugging Face key):
// doctor --fix's download turns Ollama's layers into the folder ptts loads.
// The tensors keep their values and order, the voices stay out of the model,
// the default voice comes out byte for byte as Hugging Face publishes it, and
// the config, tokenizer and CC BY-NC licence are kept.
func TestMultilingualFromOllamaRegistry(t *testing.T) {
	e := &engine.Engine{Paths: config.Paths{Home: t.TempDir()}, HTTP: http.DefaultClient, Registry: testutil.Registry(t)}
	if e.MultilingualInstalled() {
		t.Fatal("installed before the download")
	}
	if err := e.InstallMultilingual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.MultilingualInstalled() {
		t.Fatal("not installed after the download")
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(e.MultilingualDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	voice := sha256.Sum256(read("default-voice.safetensors"))
	if got := hex.EncodeToString(voice[:]); got != hfDefaultVoice {
		t.Errorf("default-voice.safetensors differs from Hugging Face's: %s", got)
	}

	model := read("model.safetensors")
	want := []string{"flow_lm.bos_emb", "flow_lm.condition_provider.conditioners.duration_delta.learnt_padding",
		"flow_lm.emb_mean", "flow_lm.out_eos.bias", "mimi.decoder.model.11.conv.bias"}
	if got := tensorNames(t, model); !slices.Equal(got, want) {
		t.Fatalf("model tensors %v, want %v (manifest order, no voices)", got, want)
	}
	if n := binary.LittleEndian.Uint64(model); (8+n)%8 != 0 {
		t.Errorf("tensor data starts at %d, not on an 8-byte boundary", 8+n)
	}
	for _, name := range want { // BF16 and F32 layers alike
		got, gotShape, err := testutil.Tensor(model, name)
		if err != nil {
			t.Fatal(err)
		}
		orig, shape, err := testutil.Tensor(testutil.OllamaBlob(t, name), name)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, orig) || !slices.Equal(gotShape, shape) {
			t.Errorf("%s changed in the merge", name)
		}
	}

	for _, f := range []string{"config.json", "tokenizer.json"} {
		if !bytes.Equal(read(f), testutil.OllamaBlob(t, f)) {
			t.Errorf("%s is not the layer's bytes", f)
		}
	}
	if lic := string(read("LICENSE")); !strings.Contains(lic, "(CC BY-NC 4.0)") {
		t.Errorf("the model licence is missing:\n%.300s", lic)
	}
	if _, err := os.Stat(filepath.Join(e.MultilingualDir(), "generation_config.json")); err == nil {
		t.Error("generation_config.json is not something ptts reads")
	}
	if _, err := os.Stat(filepath.Join(e.Paths.ModelsDir(), ".download-"+engine.MultilingualName)); err == nil {
		t.Error("the download folder was left behind")
	}
}

// Intent (AGENTS.md: release assets are pinned by size and SHA-256): a blob
// that does not match its digest in the pinned manifest is refused, and no
// model folder appears.
func TestMultilingualRefusesATamperedBlob(t *testing.T) {
	reg := testutil.Registry(t)
	voice := sha256.Sum256(testutil.OllamaBlob(t, "voices.default"))
	// Passes everything through from the fixture registry, but flips one byte
	// of the default voice.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, reg.Base+r.URL.Path, nil)
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if strings.HasSuffix(r.URL.Path, hex.EncodeToString(voice[:])) {
			b[len(b)-1] ^= 1
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
	}))
	t.Cleanup(proxy.Close)

	e := &engine.Engine{Paths: config.Paths{Home: t.TempDir()}, HTTP: http.DefaultClient,
		Registry: engine.Registry{Base: proxy.URL, Manifest: reg.Manifest}}
	err := e.InstallMultilingual(context.Background())
	if err == nil || !strings.Contains(err.Error(), "voices.default") || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum refusal for voices.default, got %v", err)
	}
	if _, err := os.Stat(e.MultilingualDir()); err == nil || e.MultilingualInstalled() {
		t.Error("a model folder appeared after a refused download")
	}
}

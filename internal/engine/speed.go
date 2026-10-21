package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Speed on the Core ML route.
//
// Phonon's speed is its duration_delta conditioner. The Python runtime takes
// it per call, but a Core ML bundle bakes the conditioning into
// host.safetensors as one vector, flow_lm.conditions: the sum of every
// conditioner's output at the bundle's values (duration_delta 0 for the
// released bundle). The duration_delta conditioner is linear with no bias:
//
//	cond(v) = W · sinEmbedding(scale·v)      W is [512 × 128]
//
// so a bundle at another speed is the released one with
//
//	conditions += W · (sinEmbedding(scale·v) − sinEmbedding(0))
//
// W is read once from the checkpoint the bundle was exported from (a 128 KiB
// range of it, not the whole file) and cached beside the bundle. Each speed
// gets its own folder that links to the released bundle's files and carries
// its own host.safetensors.

const (
	ddWeight = "flow_lm.condition_provider.conditioners.duration_delta.output_proj.weight"
	// From the checkpoint's config.json: the conditioner's scale_factor, dim
	// and max_period.
	ddScale     = 1000.0
	ddDim       = 128
	ddMaxPeriod = 10000.0
	modelDim    = 512
)

// SpeedDir returns the bundle folder for a speed, creating it if needed.
// Speed 0 is the released bundle itself.
func (e *Engine) SpeedDir(ctx context.Context, speed float64) (string, error) {
	base := filepath.Join(e.Paths.ModelsDir(), BundleName)
	if _, err := os.Stat(filepath.Join(base, "bundle.json")); err != nil {
		return "", errMissing("the English model bundle is not installed")
	}
	if speed == 0 {
		return base, nil
	}
	dir := e.SpeedPath(speed)
	if _, err := os.Stat(filepath.Join(dir, "host.safetensors")); err == nil {
		return dir, nil
	}
	w, err := e.durationWeights(ctx)
	if err != nil {
		return "", err
	}
	host, err := os.ReadFile(filepath.Join(base, "host.safetensors"))
	if err != nil {
		return "", err
	}
	if err := shiftConditions(host, w, speed); err != nil {
		return "", err
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	for _, ent := range entries {
		name := ent.Name()
		// Compiled models (.mlmodelc) are not shared: Core ML compiles each
		// folder's packages beside them on first load.
		if name == "host.safetensors" || strings.HasSuffix(name, ".mlmodelc") {
			continue
		}
		if err := os.Symlink(filepath.Join(base, name), filepath.Join(tmp, name)); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "host.safetensors"), host, 0o644); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// SpeedPath is where the bundle for a speed lives, whether or not it exists.
func (e *Engine) SpeedPath(speed float64) string {
	base := filepath.Join(e.Paths.ModelsDir(), BundleName)
	if speed == 0 {
		return base
	}
	return base + "@speed" + strconv.FormatFloat(speed, 'f', -1, 64)
}

// shiftConditions adds the duration_delta change to host.safetensors'
// flow_lm.conditions in place.
func shiftConditions(host []byte, w []float32, speed float64) error {
	off, n, err := tensorRange(host, "flow_lm.conditions", "F32")
	if err != nil {
		return fmt.Errorf("host.safetensors: %w", err)
	}
	if n != modelDim*4 {
		return fmt.Errorf("host.safetensors: flow_lm.conditions is %d bytes, want %d", n, modelDim*4)
	}
	delta := durationDelta(w, speed)
	for i := 0; i < modelDim; i++ {
		p := host[off+i*4 : off+i*4+4]
		v := math.Float32frombits(binary.LittleEndian.Uint32(p)) + delta[i]
		binary.LittleEndian.PutUint32(p, math.Float32bits(v))
	}
	return nil
}

// durationDelta is W · (sinEmbedding(scale·v) − sinEmbedding(0)).
func durationDelta(w []float32, v float64) []float32 {
	at, zero := DurationCondition(w, v), DurationCondition(w, 0)
	for i := range at {
		at[i] -= zero[i]
	}
	return at
}

// DurationCondition is the duration_delta conditioner's output at v:
// W · sinEmbedding(scale·v), [512].
func DurationCondition(w []float32, v float64) []float32 {
	emb := sinEmbedding(ddScale * v)
	out := make([]float32, modelDim)
	for o := 0; o < modelDim; o++ {
		var s float64
		for i := 0; i < ddDim; i++ {
			s += float64(w[o*ddDim+i]) * emb[i]
		}
		out[o] = float32(s)
	}
	return out
}

// sinEmbedding matches ptts' conditioners.rs: cosines then sines of
// position / max_period^(i / (half − 1)).
func sinEmbedding(position float64) []float64 {
	half := ddDim / 2
	out := make([]float64, ddDim)
	for i := 0; i < half; i++ {
		p := position / math.Pow(ddMaxPeriod, float64(i)/float64(half-1))
		out[i], out[half+i] = math.Cos(p), math.Sin(p)
	}
	return out
}

// durationWeights returns W as float32, from the cache or the checkpoint.
func (e *Engine) durationWeights(ctx context.Context) ([]float32, error) {
	cache := filepath.Join(e.Paths.ModelsDir(), BundleName+".duration_delta.f32")
	if b, err := os.ReadFile(cache); err == nil && len(b) == modelDim*ddDim*4 {
		w := make([]float32, modelDim*ddDim)
		for i := range w {
			w[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return w, nil
	}
	w, err := fetchDurationWeights(ctx, e.HTTP, e.checkpointURL())
	if err != nil {
		return nil, fmt.Errorf("reading the speed setting from the model: %w", err)
	}
	b := make([]byte, len(w)*4)
	for i, v := range w {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	if err := os.WriteFile(cache, b, 0o644); err != nil {
		return nil, err
	}
	return w, nil
}

func (e *Engine) checkpointURL() string {
	if e.CheckpointURL != "" {
		return e.CheckpointURL
	}
	return Checkpoint
}

// fetchDurationWeights reads the safetensors header, then only the
// conditioner's bytes, with HTTP range requests.
func fetchDurationWeights(ctx context.Context, c *http.Client, url string) ([]float32, error) {
	head, err := fetchRange(ctx, c, url, 0, 8)
	if err != nil {
		return nil, err
	}
	n := int64(binary.LittleEndian.Uint64(head))
	if n <= 0 || n > 10<<20 {
		return nil, fmt.Errorf("unexpected safetensors header size %d", n)
	}
	hdr, err := fetchRange(ctx, c, url, 8, n)
	if err != nil {
		return nil, err
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(hdr, &meta); err != nil {
		return nil, fmt.Errorf("safetensors header: %w", err)
	}
	var t struct {
		Dtype   string  `json:"dtype"`
		Shape   []int   `json:"shape"`
		Offsets []int64 `json:"data_offsets"`
	}
	raw, ok := meta[ddWeight]
	if !ok {
		return nil, fmt.Errorf("the checkpoint has no %s", ddWeight)
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	if len(t.Shape) != 2 || t.Shape[0] != modelDim || t.Shape[1] != ddDim || len(t.Offsets) != 2 {
		return nil, fmt.Errorf("%s has shape %v, want [%d %d]", ddWeight, t.Shape, modelDim, ddDim)
	}
	data, err := fetchRange(ctx, c, url, 8+n+t.Offsets[0], t.Offsets[1]-t.Offsets[0])
	if err != nil {
		return nil, err
	}
	return decode(data, t.Dtype, modelDim*ddDim)
}

func decode(b []byte, dtype string, count int) ([]float32, error) {
	w := make([]float32, count)
	switch dtype {
	case "BF16":
		if len(b) != count*2 {
			return nil, fmt.Errorf("got %d bytes, want %d", len(b), count*2)
		}
		for i := range w {
			// bfloat16 is the top half of a float32.
			w[i] = math.Float32frombits(uint32(binary.LittleEndian.Uint16(b[i*2:])) << 16)
		}
	case "F32":
		if len(b) != count*4 {
			return nil, fmt.Errorf("got %d bytes, want %d", len(b), count*4)
		}
		for i := range w {
			w[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
	default:
		return nil, fmt.Errorf("unsupported dtype %s", dtype)
	}
	return w, nil
}

func fetchRange(ctx context.Context, c *http.Client, url string, off, n int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("range request answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != n {
		return nil, fmt.Errorf("range request returned %d bytes, want %d", len(b), n)
	}
	return b, nil
}

// tensorRange finds a tensor's byte range in a safetensors file.
func tensorRange(file []byte, name, dtype string) (off, n int, err error) {
	if len(file) < 8 {
		return 0, 0, errors.New("file too short")
	}
	hn := int(binary.LittleEndian.Uint64(file))
	if hn <= 0 || 8+hn > len(file) {
		return 0, 0, errors.New("bad header size")
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(file[8:8+hn], &meta); err != nil {
		return 0, 0, err
	}
	var t struct {
		Dtype   string `json:"dtype"`
		Offsets []int  `json:"data_offsets"`
	}
	raw, ok := meta[name]
	if !ok {
		return 0, 0, fmt.Errorf("no tensor %s", name)
	}
	if err := json.Unmarshal(raw, &t); err != nil || len(t.Offsets) != 2 || t.Dtype != dtype {
		return 0, 0, fmt.Errorf("tensor %s is not %s", name, dtype)
	}
	start, end := 8+hn+t.Offsets[0], 8+hn+t.Offsets[1]
	if end > len(file) || start > end {
		return 0, 0, fmt.Errorf("tensor %s is out of range", name)
	}
	return start, end - start, nil
}

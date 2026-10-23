package engine_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
)

// Intent (spec: a Swift helper runs Phonon through the pinned xn-ptts v0.4.0
// Core ML runtime): the embedded helper source downloads, verifies and builds
// against that release. It needs the network and swiftc, so it runs only with
// TTS_AXI_BUILD_HELPER=1 (CI sets it).
func TestHelperBuildsAgainstThePinnedRuntime(t *testing.T) {
	if os.Getenv("TTS_AXI_BUILD_HELPER") != "1" {
		t.Skip("set TTS_AXI_BUILD_HELPER=1 to download the runtime and build the helper")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc is not installed")
	}
	e := &engine.Engine{Paths: config.Paths{Home: t.TempDir()}, HTTP: http.DefaultClient}
	ctx := context.Background()
	if err := e.InstallRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.BuildHelper(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.HelperCurrent() {
		t.Fatal("the helper was built but is not marked current")
	}
	// The helper answers a bad job with an error event and exit 1, which
	// proves it links and starts without loading a model.
	cmd := exec.Command(e.Paths.HelperBin())
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err == nil || len(out) == 0 {
		t.Errorf("want an error event for an empty job, got %q (%v)", out, err)
	}
}

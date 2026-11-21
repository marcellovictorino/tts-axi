package slidev

import (
	"strconv"
	"strings"
	"testing"
)

// Intent (spec item 4, from the branch review): the video keeps the whole
// narration. The browser sends no frames while nothing moves, so when the
// last slide stays still, its frame must stay up until the voice ends.
func TestConcatListHoldsTheLastFrameToTheEnd(t *testing.T) {
	fr := Frames{Start: 100, Frames: []float64{99.8, 100.4, 101.0}} // nothing moved after 1 s
	var total float64
	for _, l := range strings.Split(ConcatList(fr, 10), "\n") {
		if d, ok := strings.CutPrefix(l, "duration "); ok {
			v, _ := strconv.ParseFloat(d, 64)
			total += v
		}
	}
	if total < 10 {
		t.Errorf("the frames cover %.2f s of a 10 s narration", total)
	}
}

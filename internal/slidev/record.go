package slidev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/guard"
)

// Recording is what a deck recording needs.
type Recording struct {
	Node    string  // node
	Slidev  string  // Slidev's slidev.mjs
	Chrome  string  // the Chrome for Testing executable
	Deck    string  // a narrated deck folder inside Slidev's install folder
	Frames  string  // where the frames go
	Seconds float64 // the narration's length

	Sampler guard.Sampler
	Limits  guard.Limits
}

// Frames is the recorder's frames.json: when narration started and each
// frame's timestamp, both in seconds on the same clock.
type Frames struct {
	Start  float64   `json:"start"`
	Frames []float64 `json:"frames"`
}

// Record runs record.mjs under the memory guard, which stops Slidev and
// Chrome with it if memory runs short, and returns the frames it kept.
func Record(ctx context.Context, r Recording) (Frames, error) {
	var fr Frames
	script := filepath.Join(r.Frames, "record.mjs")
	if err := os.MkdirAll(r.Frames, 0o755); err != nil {
		return fr, err
	}
	if err := os.WriteFile(script, Recorder, 0o644); err != nil {
		return fr, err
	}
	port, err := freePort()
	if err != nil {
		return fr, err
	}
	base, err := r.Sampler.Sample()
	if err != nil {
		return fr, fmt.Errorf("reading memory: %w", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Node, script, r.Deck, r.Frames, r.Chrome, r.Slidev, fmt.Sprint(port), fmt.Sprintf("%.3f", r.Seconds))
	cmd.Dir = r.Deck
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := guard.Run(ctx, r.Sampler, r.Limits, base, cmd)
	if guard.IsStop(runErr) || ctx.Err() != nil {
		return fr, runErr
	}
	var res struct {
		Error  string `json:"error"`
		Server string `json:"server"`
	}
	_ = json.Unmarshal(lastLine(stdout.String()), &res)
	if runErr != nil || res.Error != "" {
		msg := res.Error
		if msg == "" {
			msg = strings.TrimSpace(firstLines(stderr.String(), 3))
		}
		if res.Server != "" {
			msg += " (Slidev: " + res.Server + ")"
		}
		return fr, fmt.Errorf("recording failed: %s", msg)
	}
	data, err := os.ReadFile(filepath.Join(r.Frames, "frames.json"))
	if err != nil {
		return fr, err
	}
	if err := json.Unmarshal(data, &fr); err != nil {
		return fr, fmt.Errorf("frames.json: %w", err)
	}
	if len(fr.Frames) == 0 {
		return fr, fmt.Errorf("recording failed: the browser sent no frames")
	}
	return fr, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func lastLine(s string) []byte {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return []byte(lines[len(lines)-1])
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}

// ConcatList is an ffmpeg concat list that shows each frame for as long as
// the browser showed it, from the moment narration started. The frame on
// screen at that moment opens the video.
func ConcatList(fr Frames) string {
	first := 0
	for i, t := range fr.Frames {
		if t <= fr.Start {
			first = i
		}
	}
	var b strings.Builder
	for i := first; i < len(fr.Frames); i++ {
		from := fr.Frames[i]
		if i == first {
			from = fr.Start
		}
		next := fr.Frames[i] + 0.5
		if i+1 < len(fr.Frames) {
			next = fr.Frames[i+1]
		}
		fmt.Fprintf(&b, "file 'f%05d.jpg'\nduration %.4f\n", i, max(next-from, 0.001))
	}
	// The concat demuxer ignores the last duration unless the file repeats.
	fmt.Fprintf(&b, "file 'f%05d.jpg'\n", len(fr.Frames)-1)
	return b.String()
}

// Mux joins the frames and the narration into an H.264 MP4 with AAC sound,
// keeping the narration's chapter marks. The video ends with the narration.
// Screencast JPEGs are full range; the video is converted to the TV range
// players expect.
func Mux(ctx context.Context, ffmpeg, framesDir string, fr Frames, audioPath, out string) error {
	list := filepath.Join(framesDir, "list.txt")
	if err := os.WriteFile(list, []byte(ConcatList(fr)), 0o644); err != nil {
		return err
	}
	args := []string{"-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", list, "-i", audioPath,
		"-map", "0:v", "-map", "1:a", "-map_metadata", "1", "-map_chapters", "1",
		"-vf", "fps=24,scale=out_range=tv,format=yuv420p", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-crf", "22", "-preset", "medium",
		"-c:a", "aac", "-b:a", "128k", "-shortest", "-movflags", "+faststart", out}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %s", firstLines(stderr.String(), 3))
	}
	return nil
}

// Chapter is a slide's place in the video.
type Chapter struct {
	Title string
	Start time.Duration
}

// YouTubeChapters writes chapter lines for a YouTube description: one
// "m:ss Title" line per slide, the first at 0:00.
func YouTubeChapters(chs []Chapter) string {
	var b strings.Builder
	for _, c := range chs {
		fmt.Fprintf(&b, "%s %s\n", clock(c.Start), c.Title)
	}
	return b.String()
}

func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Caption is one spoken line's place in the video.
type Caption struct {
	Text       string
	Start, End time.Duration
}

// SRT writes captions in SubRip form, which YouTube and video players read.
func SRT(cs []Caption) string {
	var b strings.Builder
	for i, c := range cs {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(c.Start), srtTime(c.End), c.Text)
	}
	return b.String()
}

func srtTime(d time.Duration) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

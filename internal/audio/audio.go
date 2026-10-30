// Package audio reads and writes the WAV files the helpers produce, makes
// silence, and asks ffmpeg to join clips into one file with chapter marks.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Phonon speaks 24 kHz mono; the helpers write 16-bit PCM.
const (
	SampleRate = 24000
	bytesPer   = 2
)

// Formats are the output containers tts-axi writes.
var Formats = []string{"mp3", "m4a", "wav"}

// FormatOf returns the format implied by a file name, or "".
func FormatOf(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	for _, f := range Formats {
		if f == ext {
			return f
		}
	}
	return ""
}

// Duration reads a WAV file's length from its header.
func Duration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return 0, fmt.Errorf("%s: not a WAV file", path)
	}
	var rate, align uint32
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return 0, fmt.Errorf("%s: no data chunk", path)
		}
		id, size := string(hdr[0:4]), binary.LittleEndian.Uint32(hdr[4:8])
		switch id {
		case "fmt ":
			buf := make([]byte, size)
			if _, err := io.ReadFull(f, buf); err != nil || size < 16 {
				return 0, fmt.Errorf("%s: bad fmt chunk", path)
			}
			rate = binary.LittleEndian.Uint32(buf[4:8])
			align = uint32(binary.LittleEndian.Uint16(buf[12:14]))
		case "data":
			if rate == 0 || align == 0 {
				return 0, fmt.Errorf("%s: data before fmt", path)
			}
			frames := int64(size) / int64(align)
			return time.Duration(frames) * time.Second / time.Duration(rate), nil
		default:
			if _, err := f.Seek(int64(size+size%2), io.SeekCurrent); err != nil {
				return 0, err
			}
		}
	}
}

// WriteSilence writes d of silence as a 24 kHz mono 16-bit WAV.
func WriteSilence(path string, d time.Duration) error {
	frames := int(d.Seconds() * SampleRate)
	return os.WriteFile(path, wavBytes(make([]byte, frames*bytesPer)), 0o644)
}

func wavBytes(pcm []byte) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(pcm)))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(SampleRate))
	w(uint32(SampleRate * bytesPer))
	w(uint16(bytesPer))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(len(pcm)))
	b.Write(pcm)
	return b.Bytes()
}

// Chapter is a named span of the joined file.
type Chapter struct {
	Title      string
	Start, End time.Duration
}

// Meta is what goes into the file's tags.
type Meta struct {
	Title  string
	Artist string
	Album  string
}

// Join concatenates WAV clips (all 24 kHz mono 16-bit) into out, encoded by
// out's extension, with chapter marks and tags. workDir holds the ffmpeg
// list files.
func Join(ctx context.Context, ffmpeg, workDir string, clips []string, chapters []Chapter, meta Meta, out string) error {
	if len(clips) == 0 {
		return fmt.Errorf("nothing to join")
	}
	var list strings.Builder
	for _, c := range clips {
		// The concat demuxer's list quotes paths with single quotes.
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(c, "'", `'\''`))
	}
	listPath := filepath.Join(workDir, "concat-"+filepath.Base(out)+".txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}
	metaPath := filepath.Join(workDir, "meta-"+filepath.Base(out)+".txt")
	if err := os.WriteFile(metaPath, []byte(FFMetadata(chapters, meta)), 0o644); err != nil {
		return err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-f", "ffmetadata", "-i", metaPath,
		"-map", "0:a", "-map_metadata", "1", "-map_chapters", "1"}
	args = append(args, codecArgs(FormatOf(out))...)
	args = append(args, out)
	return runFFmpeg(ctx, ffmpeg, args)
}

// Encode converts one WAV clip to out's format (a plain copy for .wav).
func Encode(ctx context.Context, ffmpeg, wav, out string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", wav}
	args = append(args, codecArgs(FormatOf(out))...)
	return runFFmpeg(ctx, ffmpeg, append(args, out))
}

func codecArgs(format string) []string {
	switch format {
	case "m4a":
		return []string{"-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart"}
	case "wav":
		return []string{"-c:a", "pcm_s16le"}
	default: // mp3
		return []string{"-c:a", "libmp3lame", "-q:a", "4", "-id3v2_version", "3"}
	}
}

func runFFmpeg(ctx context.Context, ffmpeg string, args []string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", lastLine(msg))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// FFMetadata renders ffmpeg's metadata file format: global tags, then one
// [CHAPTER] block per chapter with millisecond times.
func FFMetadata(chapters []Chapter, meta Meta) string {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	tag := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, escapeMeta(v))
		}
	}
	tag("title", meta.Title)
	tag("artist", meta.Artist)
	tag("album", meta.Album)
	for _, c := range chapters {
		fmt.Fprintf(&b, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\n", c.Start.Milliseconds(), c.End.Milliseconds())
		tag("title", c.Title)
	}
	return b.String()
}

func escapeMeta(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", `\`+"\n")
	return r.Replace(s)
}

// TrimSilence removes near-silent audio from both ends of a 24 kHz mono
// 16-bit WAV written by a helper, keeping pad of it, and rewrites the file.
// Phonon ends each utterance with some silence of its own; trimming it makes
// the gaps and [pause] lengths tts-axi inserts the real ones.
func TrimSilence(path string, pad time.Duration) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pcm, err := pcmData(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	const threshold = 500 // of 32767: about -36 dB
	n := len(pcm) / bytesPer
	sample := func(i int) int {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i*bytesPer:])))
		if v < 0 {
			return -v
		}
		return v
	}
	first, last := 0, n-1
	for first < n && sample(first) < threshold {
		first++
	}
	for last > first && sample(last) < threshold {
		last--
	}
	if first >= n {
		return nil // all silence: leave it
	}
	keep := int(pad.Seconds() * SampleRate)
	first = max(0, first-keep)
	last = min(n-1, last+keep)
	return os.WriteFile(path, wavBytes(pcm[first*bytesPer:(last+1)*bytesPer]), 0o644)
}

// pcmData returns the data chunk of a 16-bit mono 24 kHz WAV.
func pcmData(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAV file")
	}
	off := 12
	okFmt := false
	for off+8 <= len(data) {
		id, size := string(data[off:off+4]), int(binary.LittleEndian.Uint32(data[off+4:off+8]))
		body := off + 8
		if body+size > len(data) {
			size = len(data) - body // a helper killed mid-write; take what is there
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("bad fmt chunk")
			}
			ch := binary.LittleEndian.Uint16(data[body+2:])
			rate := binary.LittleEndian.Uint32(data[body+4:])
			bits := binary.LittleEndian.Uint16(data[body+14:])
			if ch != 1 || rate != SampleRate || bits != 16 {
				return nil, fmt.Errorf("want 24 kHz mono 16-bit, got %d Hz, %d channels, %d bits", rate, ch, bits)
			}
			okFmt = true
		case "data":
			if !okFmt {
				return nil, fmt.Errorf("data before fmt")
			}
			return data[body : body+size-size%bytesPer], nil
		}
		off = body + size + size%2
	}
	return nil, fmt.Errorf("no data chunk")
}

// ToReference converts any audio file ffmpeg can read into a 24 kHz mono
// 16-bit WAV, the form a cloned voice's reference clip is stored in.
func ToReference(ctx context.Context, ffmpeg, in, out string) error {
	return runFFmpeg(ctx, ffmpeg, []string{"-y", "-v", "error", "-i", in, "-vn", "-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", out})
}

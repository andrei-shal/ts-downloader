package merge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	ffmpeg, err := FindFFmpeg("")
	if err != nil {
		t.Skip(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not found")
	}

	// Directory name with a quote checks escaping in the concat list.
	dir := filepath.Join(t.TempDir(), "it's parts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var files []string
	for i := range 3 {
		f := filepath.Join(dir, fmt.Sprintf("%d.ts", i))
		out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=25",
			"-f", "lavfi", "-i", "sine=duration=1",
			"-c:v", "libx264", "-c:a", "aac", "-shortest",
			"-output_ts_offset", strconv.Itoa(i), f).CombinedOutput()
		if err != nil {
			t.Fatalf("make segment: %v %s", err, out)
		}
		files = append(files, f)
	}

	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := Merge(context.Background(), ffmpeg, files, out); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if d < 2.8 || d > 3.3 {
		t.Fatalf("duration %v, want ~3s", d)
	}
}

// Real HLS segments carry continuous timestamps. Merging must keep them:
// the old concat-demuxer merge added ~70 ms at every segment border, so a
// 2-hour film became ~50 s longer and subtitles drifted.
func TestMergeHLSKeepsTimeline(t *testing.T) {
	ffmpeg, err := FindFFmpeg("")
	if err != nil {
		t.Skip(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not found")
	}

	dir := t.TempDir()
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=160x120:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=duration=30:sample_rate=44100",
		"-c:v", "libx264", "-g", "48", "-c:a", "aac", "-shortest",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "seg%03d.ts"),
		filepath.Join(dir, "index.m3u8")).CombinedOutput(); err != nil {
		t.Fatalf("make hls: %v %s", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "seg*.ts"))
	if len(files) < 10 {
		t.Fatalf("only %d segments", len(files))
	}

	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := Merge(context.Background(), ffmpeg, files, out); err != nil {
		t.Fatal(err)
	}

	probe := func(args ...string) string {
		b, err := exec.Command(ffprobe, append([]string{"-v", "error"}, args...)...).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	frames, _ := strconv.Atoi(probe("-select_streams", "v:0", "-count_packets",
		"-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", out))
	dur, _ := strconv.ParseFloat(probe("-select_streams", "v:0",
		"-show_entries", "stream=duration", "-of", "csv=p=0", out), 64)
	want := float64(frames) * 1001 / 24000
	if d := dur - want; d > 0.1 || d < -0.1 {
		t.Fatalf("video duration %.3fs, but %d frames last %.3fs: timeline stretched by %.3fs",
			dur, frames, want, d)
	}
}

func TestFindFFmpegMissing(t *testing.T) {
	if _, err := FindFFmpeg("/nonexistent/ffmpeg"); err == nil {
		t.Fatal("expected error")
	}
}

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

func TestFindFFmpegMissing(t *testing.T) {
	if _, err := FindFFmpeg("/nonexistent/ffmpeg"); err == nil {
		t.Fatal("expected error")
	}
}

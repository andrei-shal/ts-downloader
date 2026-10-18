package subs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeWhisper mimics whisper-cli: prints progress to stderr and writes <-of>.srt.
// It also saves its arguments so the test can check them.
const fakeWhisper = `#!/bin/sh
echo "$@" > "$(dirname "$0")/args.txt"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-of" ]; then out="$2"; fi
  shift
done
echo "whisper_print_progress_callback: progress =  50%" >&2
echo "[00:00:00.000 --> 00:00:01.000]  Hello"
echo "whisper_print_progress_callback: progress = 100%" >&2
printf '1\n00:00:00,000 --> 00:00:01,000\nHello there\n\n' > "$out.srt"
`

func TestGenerateAndEmbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake whisper is a shell script")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not found")
	}

	dir := t.TempDir()
	video := filepath.Join(dir, "my video.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=25",
		"-f", "lavfi", "-i", "sine=duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", video).CombinedOutput(); err != nil {
		t.Fatalf("make video: %v %s", err, out)
	}

	binDir := t.TempDir()
	whisper := filepath.Join(binDir, "whisper-cli")
	if err := os.WriteFile(whisper, []byte(fakeWhisper), 0o755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(binDir, "model.bin")
	os.WriteFile(model, []byte("x"), 0o644)

	var progress []int
	o := Options{FFmpeg: ffmpeg, Whisper: whisper, Model: model, Lang: "ru", Translate: true,
		OnProgress: func(p int) { progress = append(progress, p) }}
	srt, err := Generate(context.Background(), video, o)
	if err != nil {
		t.Fatal(err)
	}
	if srt != filepath.Join(dir, "my video.srt") {
		t.Fatalf("srt path %q", srt)
	}
	if len(progress) != 2 || progress[1] != 100 {
		t.Fatalf("progress %v", progress)
	}
	args, _ := os.ReadFile(filepath.Join(binDir, "args.txt"))
	for _, want := range []string{"-l ru", "-tr", "-osrt", "-m " + model} {
		if !strings.Contains(string(args), want) {
			t.Errorf("whisper args %q missing %q", args, want)
		}
	}

	if err := Embed(context.Background(), ffmpeg, video, srt, SubLang(o)); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries",
		"stream=codec_type,codec_name:stream_tags=language", "-of", "csv=p=0", video).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"video", "audio", "mov_text,subtitle,eng"} {
		if !strings.Contains(s, want) {
			t.Errorf("ffprobe output %q missing %q", s, want)
		}
	}
}

func TestMissingModel(t *testing.T) {
	_, err := Generate(context.Background(), "x.mp4", Options{Model: "/nonexistent.bin"})
	if err == nil || !strings.Contains(err.Error(), "модели") {
		t.Fatalf("got %v", err)
	}
}

// Package merge joins .ts segments into one .mp4 with ffmpeg, without re-encoding.
package merge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// FindFFmpeg returns the ffmpeg executable: custom if set, otherwise from PATH.
func FindFFmpeg(custom string) (string, error) {
	name := strings.TrimSpace(custom)
	if name == "" {
		name = "ffmpeg"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("ffmpeg не найден (%s): установите его или укажите путь в настройках", name)
	}
	return path, nil
}

// Merge joins the segments into out.
//
// HLS segments are pieces of one continuous MPEG-TS stream, so they are fed to
// ffmpeg back to back as a single stream (the way players read them) and their
// own timestamps are kept. ffmpeg's concat demuxer must not be used here: it
// places every file at the sum of the previous files' probed durations, which
// added ~70 ms at each border (≈50 s over a 2-hour film) and made subtitles drift.
func Merge(ctx context.Context, ffmpeg string, files []string, out string) error {
	if len(files) == 0 {
		return errors.New("нет сегментов для склейки")
	}
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "mpegts", "-i", "pipe:0",
		// Only video and audio: HLS often carries ID3 data streams that mp4 cannot hold.
		"-map", "0:v?", "-map", "0:a?",
		"-c", "copy", "-movflags", "+faststart", out)
	HideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск ffmpeg: %w", err)
	}

	writeErr := make(chan error, 1)
	go func() {
		writeErr <- feed(stdin, files)
	}()

	waitErr := cmd.Wait()
	werr := <-writeErr
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("ffmpeg: %v\n%s", waitErr, tail(stderr.String(), 2000))
	}
	// ffmpeg succeeded, but a segment could not be read: the result would be incomplete.
	if werr != nil {
		return werr
	}
	return nil
}

// feed writes all files into w one after another and closes w.
func feed(w io.WriteCloser, files []string) error {
	defer w.Close()
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			return fmt.Errorf("чтение сегмента: %w", err)
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			// A write error means ffmpeg exited early; its own error is reported instead.
			if errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE) {
				return nil
			}
			return fmt.Errorf("передача сегмента %s в ffmpeg: %w", filepath.Base(name), err)
		}
	}
	return nil
}

// RunFFmpeg runs ffmpeg quietly (overwriting outputs) and returns its error output on failure.
func RunFFmpeg(ctx context.Context, ffmpeg string, args ...string) error {
	cmd := exec.CommandContext(ctx, ffmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	HideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %v\n%s", err, tail(stderr.String(), 2000))
	}
	return nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

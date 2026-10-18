// Package merge joins .ts segments into one .mp4 with ffmpeg, without re-encoding.
package merge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Merge writes a concat list next to the segments and runs ffmpeg to produce out.
func Merge(ctx context.Context, ffmpeg string, files []string, out string) error {
	if len(files) == 0 {
		return errors.New("нет сегментов для склейки")
	}
	var list strings.Builder
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return err
		}
		// Inside single quotes everything is literal except ', which is written as '\''.
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(abs), "'", `'\''`))
	}
	listPath := filepath.Join(filepath.Dir(files[0]), "list.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}

	return RunFFmpeg(ctx, ffmpeg,
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", "-movflags", "+faststart", out)
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

// Package subs generates subtitles from a video's audio with whisper.cpp
// and embeds them into the mp4 as a soft subtitle track.
package subs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"ts-downloader/internal/merge"
)

// FindWhisper returns the whisper.cpp CLI: custom if set, otherwise whisper-cli from PATH.
func FindWhisper(custom string) (string, error) {
	names := []string{"whisper-cli", "whisper-cpp"}
	if c := strings.TrimSpace(custom); c != "" {
		names = []string{c}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("whisper-cli не найден (%s): установите whisper.cpp или укажите путь", strings.Join(names, ", "))
}

type Options struct {
	FFmpeg    string
	Whisper   string
	Model     string // path to a ggml model, e.g. ggml-large-v3-turbo.bin
	Lang      string // "auto", "en", "ru", ...
	Translate bool   // translate speech to English
	Threads   int    // 0 = all CPUs

	OnProgress func(percent int) // may be nil
}

// Generate extracts the audio of video and writes <video without ext>.srt next to it.
func Generate(ctx context.Context, video string, o Options) (string, error) {
	if st, err := os.Stat(o.Model); err != nil || st.IsDir() {
		return "", fmt.Errorf("файл модели не найден: %s", o.Model)
	}
	threads := o.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	lang := o.Lang
	if lang == "" {
		lang = "auto"
	}

	tmp, err := os.MkdirTemp("", "ts-downloader-subs-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	// whisper.cpp wants 16 kHz mono PCM.
	wav := filepath.Join(tmp, "audio.wav")
	if err := merge.RunFFmpeg(ctx, o.FFmpeg, "-i", video, "-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav); err != nil {
		return "", fmt.Errorf("извлечение звука: %w", err)
	}

	base := strings.TrimSuffix(video, filepath.Ext(video))
	args := []string{"-m", o.Model, "-f", wav, "-l", lang, "-t", strconv.Itoa(threads), "-osrt", "-of", base, "-pp"}
	if o.Translate {
		args = append(args, "-tr")
	}
	if err := runWhisper(ctx, o.Whisper, args, o.OnProgress); err != nil {
		return "", err
	}
	srt := base + ".srt"
	if st, err := os.Stat(srt); err != nil || st.Size() == 0 {
		return "", errors.New("whisper не создал субтитры (возможно, в видео нет речи)")
	}
	return srt, nil
}

var progressRe = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

func runWhisper(ctx context.Context, bin string, args []string, onProgress func(int)) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	merge.HideWindow(cmd)
	cmd.Stdout = io.Discard // the transcript itself; we only need the .srt file
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск whisper: %w", err)
	}

	// Keep the last lines of stderr for the error message, report progress lines.
	var lastLines []string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := progressRe.FindStringSubmatch(line); m != nil {
			if p, err := strconv.Atoi(m[1]); err == nil && onProgress != nil {
				onProgress(p)
			}
			continue
		}
		lastLines = append(lastLines, line)
		if len(lastLines) > 15 {
			lastLines = lastLines[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("whisper: %v\n%s", err, strings.Join(lastLines, "\n"))
	}
	return nil
}

// Embed adds srt to video as a soft subtitle track (mov_text), replacing video in place.
func Embed(ctx context.Context, ffmpeg, video, srt, lang string) error {
	tmp := strings.TrimSuffix(video, filepath.Ext(video)) + ".subs-tmp.mp4"
	err := merge.RunFFmpeg(ctx, ffmpeg,
		"-i", video, "-i", srt,
		"-map", "0:v?", "-map", "0:a?", "-map", "1:0",
		"-c", "copy", "-c:s", "mov_text",
		"-metadata:s:s:0", "language="+iso639_2(lang),
		"-disposition:s:0", "default",
		"-movflags", "+faststart", tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("вшивание субтитров: %w", err)
	}
	return os.Rename(tmp, video)
}

// SubLang is the language of the resulting subtitles.
func SubLang(o Options) string {
	if o.Translate {
		return "en"
	}
	return o.Lang
}

func iso639_2(lang string) string {
	switch lang {
	case "en":
		return "eng"
	case "ru":
		return "rus"
	case "de":
		return "deu"
	case "fr":
		return "fra"
	case "es":
		return "spa"
	case "uk":
		return "ukr"
	default:
		return "und"
	}
}

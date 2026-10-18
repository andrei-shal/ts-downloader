// TS Downloader: downloads numbered .ts video segments and joins them into one .mp4.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"ts-downloader/internal/downloader"
	"ts-downloader/internal/merge"
	"ts-downloader/internal/pattern"
)

func main() {
	a := app.NewWithID("dev.tsdownloader")
	w := a.NewWindow("TS Downloader")
	newUI(a, w)
	w.Resize(fyne.NewSize(720, 640))
	w.ShowAndRun()
}

type ui struct {
	win   fyne.Window
	prefs fyne.Preferences

	base, tmpl, pad, start, end, workers *widget.Entry
	headers, outDir, outName, ffmpeg     *widget.Entry
	cleanup                              *widget.Check
	preview, status, logText             *widget.Label
	logScroll                            *container.Scroll
	bar                                  *widget.ProgressBar
	barInf                               *widget.ProgressBarInfinite
	startBtn, stopBtn                    *widget.Button

	cancel context.CancelFunc
}

func newUI(a fyne.App, w fyne.Window) {
	u := &ui{win: w, prefs: a.Preferences()}

	// Text fields are remembered between launches.
	entry := func(key, placeholder, def string) *widget.Entry {
		e := widget.NewEntry()
		e.SetPlaceHolder(placeholder)
		e.SetText(u.prefs.StringWithFallback(key, def))
		e.OnChanged = func(s string) { u.prefs.SetString(key, s); u.updatePreview() }
		return e
	}
	u.preview = widget.NewLabel("")
	u.preview.Wrapping = fyne.TextWrapBreak

	u.base = entry("base", "https://example.com/video/", "")
	u.tmpl = entry("tmpl", "seg_{}.ts", "seg_{}.ts")
	u.pad = entry("pad", "0", "0")
	u.start = entry("start", "0", "0")
	u.end = entry("end", "пусто = до первого 404", "")
	u.workers = entry("workers", "8", "8")
	u.outDir = entry("outDir", "папка", defaultDir())
	u.outName = entry("outName", "video.mp4", "video.mp4")
	u.ffmpeg = entry("ffmpeg", "пусто = ffmpeg из PATH", "")

	u.headers = widget.NewMultiLineEntry()
	u.headers.SetPlaceHolder("Referer: https://site/\nUser-Agent: Mozilla/5.0 ...")
	u.headers.SetMinRowsVisible(2)
	u.headers.SetText(u.prefs.String("headers"))
	u.headers.OnChanged = func(s string) { u.prefs.SetString("headers", s) }

	u.cleanup = widget.NewCheck("Удалить сегменты после склейки", func(b bool) { u.prefs.SetBool("cleanup", b) })
	u.cleanup.SetChecked(u.prefs.BoolWithFallback("cleanup", true))

	browse := widget.NewButton("Выбрать…", func() {
		dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
			if err == nil && dir != nil {
				u.outDir.SetText(dir.Path())
			}
		}, w)
	})

	u.status = widget.NewLabel("Готово к работе")
	u.bar = widget.NewProgressBar()
	u.barInf = widget.NewProgressBarInfinite()
	u.barInf.Stop()
	u.barInf.Hide()
	u.logText = widget.NewLabel("")
	u.logText.Wrapping = fyne.TextWrapBreak
	u.logText.Selectable = true
	u.logScroll = container.NewVScroll(u.logText)
	u.logScroll.SetMinSize(fyne.NewSize(0, 140))

	u.startBtn = widget.NewButton("Старт", u.onStart)
	u.startBtn.Importance = widget.HighImportance
	u.stopBtn = widget.NewButton("Стоп", func() {
		if u.cancel != nil {
			u.cancel()
		}
	})
	u.stopBtn.Disable()

	form := widget.NewForm(
		widget.NewFormItem("Base URL", u.base),
		widget.NewFormItem("Имя файла", u.tmpl),
		widget.NewFormItem("Ссылка", u.preview),
		widget.NewFormItem("Номера", container.NewGridWithColumns(3,
			labeled("с", u.start), labeled("по", u.end), labeled("цифр", u.pad))),
		widget.NewFormItem("Потоков", u.workers),
		widget.NewFormItem("Заголовки", u.headers),
		widget.NewFormItem("Папка", container.NewBorder(nil, nil, nil, browse, u.outDir)),
		widget.NewFormItem("Файл", u.outName),
		widget.NewFormItem("ffmpeg", u.ffmpeg),
		widget.NewFormItem("", u.cleanup),
	)

	top := container.NewVBox(
		form,
		container.NewGridWithColumns(2, u.startBtn, u.stopBtn),
		u.status,
		container.NewStack(u.bar, u.barInf),
	)
	w.SetContent(container.NewBorder(top, nil, nil, nil, u.logScroll))
	u.updatePreview()
}

func labeled(text string, e *widget.Entry) fyne.CanvasObject {
	return container.NewBorder(nil, nil, widget.NewLabel(text), nil, e)
}

func defaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if d := filepath.Join(home, "Downloads"); isDir(d) {
		return d
	}
	return home
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (u *ui) updatePreview() {
	if u.preview == nil || u.base == nil || u.tmpl == nil || u.start == nil || u.pad == nil {
		return // called while widgets are still being created
	}
	start, err1 := atoi(u.start.Text, 0)
	pad, err2 := atoi(u.pad.Text, 0)
	if err := errors.Join(err1, err2); err != nil {
		u.preview.SetText("⚠ " + err.Error())
		return
	}
	link, err := pattern.BuildURL(u.base.Text, u.tmpl.Text, start, pad)
	if err != nil {
		u.preview.SetText("⚠ " + err.Error())
		return
	}
	u.preview.SetText(link)
}

func atoi(s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("«%s» — не целое неотрицательное число", s)
	}
	return n, nil
}

func (u *ui) log(msg string) {
	line := time.Now().Format("15:04:05 ") + msg
	if u.logText.Text != "" {
		line = u.logText.Text + "\n" + line
	}
	u.logText.SetText(line)
	u.logScroll.ScrollToBottom()
}

// job is everything needed for one run, read from the form on the UI thread.
type job struct {
	dl      downloader.Config
	ffmpeg  string
	out     string
	cleanup bool
	total   int // 0 when the end is unknown
}

func (u *ui) readForm() (job, error) {
	start, err := atoi(u.start.Text, 0)
	if err != nil {
		return job{}, err
	}
	end, err := atoi(u.end.Text, downloader.NoEnd)
	if err != nil {
		return job{}, err
	}
	pad, err := atoi(u.pad.Text, 0)
	if err != nil {
		return job{}, err
	}
	workers, err := atoi(u.workers.Text, 8)
	if err != nil {
		return job{}, err
	}
	if workers < 1 || workers > 64 {
		return job{}, errors.New("потоков должно быть от 1 до 64")
	}
	if strings.TrimSpace(u.base.Text) == "" && !strings.Contains(u.tmpl.Text, "://") {
		return job{}, errors.New("укажите Base URL")
	}
	if err := pattern.Validate(u.tmpl.Text); err != nil {
		return job{}, err
	}
	headers, err := downloader.ParseHeaders(u.headers.Text)
	if err != nil {
		return job{}, err
	}
	ffmpeg, err := merge.FindFFmpeg(u.ffmpeg.Text)
	if err != nil {
		return job{}, err
	}
	dir := strings.TrimSpace(u.outDir.Text)
	if !isDir(dir) {
		return job{}, fmt.Errorf("папка не существует: %s", dir)
	}
	name := strings.TrimSpace(u.outName.Text)
	if name == "" {
		return job{}, errors.New("укажите имя файла")
	}
	if !strings.EqualFold(filepath.Ext(name), ".mp4") {
		name += ".mp4"
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))

	j := job{
		dl: downloader.Config{
			Base: u.base.Text, Template: u.tmpl.Text, Pad: pad,
			Start: start, End: end, Workers: workers, Headers: headers,
			PartsDir: filepath.Join(dir, base+"_parts"),
		},
		ffmpeg:  ffmpeg,
		out:     filepath.Join(dir, name),
		cleanup: u.cleanup.Checked,
	}
	if end != downloader.NoEnd {
		if end < start {
			return job{}, errors.New("«по» меньше, чем «с»")
		}
		j.total = end - start + 1
	}
	return j, nil
}

func (u *ui) onStart() {
	j, err := u.readForm()
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	if _, err := os.Stat(j.out); err == nil {
		dialog.ShowConfirm("Файл существует", "Перезаписать "+j.out+"?", func(ok bool) {
			if ok {
				u.run(j)
			}
		}, u.win)
		return
	}
	u.run(j)
}

func (u *ui) run(j job) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.startBtn.Disable()
	u.stopBtn.Enable()
	u.logText.SetText("")
	u.bar.SetValue(0)
	if j.total > 0 {
		u.bar.Max = float64(j.total)
		u.barInf.Stop()
		u.barInf.Hide()
		u.bar.Show()
	} else {
		u.bar.Hide()
		u.barInf.Show()
		u.barInf.Start()
	}
	u.status.SetText("Скачивание…")
	u.log("Сегменты сохраняются в " + j.dl.PartsDir)

	j.dl.OnSegment = func(done int) {
		fyne.Do(func() {
			u.status.SetText(fmt.Sprintf("Скачано сегментов: %d", done))
			if j.total > 0 {
				u.bar.SetValue(float64(done))
			}
		})
	}
	j.dl.OnLog = func(msg string) { fyne.Do(func() { u.log(msg) }) }

	go func() {
		err := u.work(ctx, j)
		fyne.Do(func() {
			cancel()
			u.cancel = nil
			u.startBtn.Enable()
			u.stopBtn.Disable()
			u.barInf.Stop()
			u.barInf.Hide()
			u.bar.Show()
			switch {
			case errors.Is(err, context.Canceled):
				u.status.SetText("Остановлено. Скачанные сегменты сохранены: при повторном старте загрузка продолжится.")
				u.log("Остановлено пользователем")
			case err != nil:
				u.status.SetText("Ошибка")
				u.log("Ошибка: " + err.Error())
				dialog.ShowError(err, u.win)
			default:
				u.bar.SetValue(u.bar.Max)
				u.status.SetText("Готово: " + j.out)
				u.log("Готово: " + j.out)
			}
		})
	}()
}

// work runs off the UI thread.
func (u *ui) work(ctx context.Context, j job) error {
	res, err := downloader.Run(ctx, j.dl)
	if err != nil {
		return err
	}
	fyne.Do(func() {
		u.log(fmt.Sprintf("Скачано %d сегментов, склеиваю в mp4…", len(res.Files)))
		u.status.SetText("Склейка…")
	})
	if err := merge.Merge(ctx, j.ffmpeg, res.Files, j.out); err != nil {
		return err
	}
	if j.cleanup {
		if err := os.RemoveAll(j.dl.PartsDir); err != nil {
			fyne.Do(func() { u.log("Не удалось удалить сегменты: " + err.Error()) })
		}
	}
	return nil
}

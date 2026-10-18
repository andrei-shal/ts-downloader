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
	"ts-downloader/internal/subs"
)

func main() {
	a := app.NewWithID("dev.tsdownloader")
	w := a.NewWindow("TS Downloader")
	newUI(a, w)
	w.Resize(fyne.NewSize(760, 820))
	w.ShowAndRun()
}

type ui struct {
	win   fyne.Window
	prefs fyne.Preferences

	base, tmpl, pad, start, end, workers *widget.Entry
	headers, outDir, outName, ffmpeg     *widget.Entry
	cleanup                              *widget.Check
	subsOn, translate                    *widget.Check
	lang                                 *widget.Select
	model, whisper                       *widget.Entry
	preview, status, logText             *widget.Label
	logScroll                            *container.Scroll
	bar                                  *widget.ProgressBar
	barInf                               *widget.ProgressBarInfinite
	startBtn, stopBtn, subsBtn           *widget.Button

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

	u.model = entry("model", "путь к ggml-*.bin", "")
	u.whisper = entry("whisper", "пусто = whisper-cli из PATH", "")
	check := func(key, text string, def bool) *widget.Check {
		c := widget.NewCheck(text, func(b bool) { u.prefs.SetBool(key, b) })
		c.SetChecked(u.prefs.BoolWithFallback(key, def))
		return c
	}
	u.subsOn = check("subsOn", "Сгенерировать субтитры (Whisper) и вшить в mp4", false)
	u.translate = check("translate", "Перевести на английский", false)
	u.lang = widget.NewSelect([]string{"auto", "en", "ru"}, func(s string) { u.prefs.SetString("lang", s) })
	u.lang.SetSelected(u.prefs.StringWithFallback("lang", "auto"))
	browseModel := widget.NewButton("Выбрать…", func() {
		dialog.ShowFileOpen(func(f fyne.URIReadCloser, err error) {
			if err == nil && f != nil {
				u.model.SetText(f.URI().Path())
				f.Close()
			}
		}, w)
	})

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
	u.subsBtn = widget.NewButton("Субтитры к готовому mp4", u.onSubsOnly)

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
		widget.NewFormItem("Субтитры", u.subsOn),
		widget.NewFormItem("Язык речи", container.NewHBox(u.lang, u.translate)),
		widget.NewFormItem("Модель", container.NewBorder(nil, nil, nil, browseModel, u.model)),
		widget.NewFormItem("whisper-cli", u.whisper),
	)

	top := container.NewVBox(
		form,
		container.NewGridWithColumns(3, u.startBtn, u.subsBtn, u.stopBtn),
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
	total   int           // 0 when the end is unknown
	subs    *subs.Options // nil = no subtitles
	only    bool          // only subtitles for an existing mp4, no download
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
	j, err := u.readOutput()
	if err != nil {
		return job{}, err
	}
	j.dl = downloader.Config{
		Base: u.base.Text, Template: u.tmpl.Text, Pad: pad,
		Start: start, End: end, Workers: workers, Headers: headers,
		PartsDir: strings.TrimSuffix(j.out, filepath.Ext(j.out)) + "_parts",
	}
	j.cleanup = u.cleanup.Checked
	if end != downloader.NoEnd {
		if end < start {
			return job{}, errors.New("«по» меньше, чем «с»")
		}
		j.total = end - start + 1
	}
	if u.subsOn.Checked {
		if j.subs, err = u.readSubs(j.ffmpeg); err != nil {
			return job{}, err
		}
	}
	return j, nil
}

// readOutput fills ffmpeg and the output .mp4 path.
func (u *ui) readOutput() (job, error) {
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
	return job{ffmpeg: ffmpeg, out: filepath.Join(dir, name)}, nil
}

func (u *ui) readSubs(ffmpeg string) (*subs.Options, error) {
	whisper, err := subs.FindWhisper(u.whisper.Text)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(u.model.Text)
	if model == "" {
		return nil, errors.New("укажите файл модели Whisper (например ggml-large-v3-turbo.bin)")
	}
	if st, err := os.Stat(model); err != nil || st.IsDir() {
		return nil, fmt.Errorf("файл модели не найден: %s", model)
	}
	return &subs.Options{
		FFmpeg: ffmpeg, Whisper: whisper, Model: model,
		Lang: u.lang.Selected, Translate: u.translate.Checked,
	}, nil
}

// onSubsOnly makes subtitles for an already downloaded mp4 (Папка + Файл).
func (u *ui) onSubsOnly() {
	j, err := u.readOutput()
	if err == nil {
		if _, statErr := os.Stat(j.out); statErr != nil {
			err = fmt.Errorf("файл не найден: %s\nУкажите папку и имя готового mp4", j.out)
		}
	}
	if err == nil {
		j.subs, err = u.readSubs(j.ffmpeg)
	}
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	j.only = true
	u.run(j)
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
	u.subsBtn.Disable()
	u.stopBtn.Enable()
	u.logText.SetText("")
	u.bar.SetValue(0)
	if j.only {
		u.bar.Max = 100
		u.barInf.Stop()
		u.barInf.Hide()
		u.bar.Show()
	} else if j.total > 0 {
		u.bar.Max = float64(j.total)
		u.barInf.Stop()
		u.barInf.Hide()
		u.bar.Show()
	} else {
		u.bar.Hide()
		u.barInf.Show()
		u.barInf.Start()
	}
	if !j.only {
		u.status.SetText("Скачивание…")
		u.log("Сегменты сохраняются в " + j.dl.PartsDir)
	}

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
			u.subsBtn.Enable()
			u.stopBtn.Disable()
			u.barInf.Stop()
			u.barInf.Hide()
			u.bar.Show()
			switch {
			case errors.Is(err, context.Canceled):
				if j.only {
					u.status.SetText("Остановлено")
				} else {
					u.status.SetText("Остановлено. Скачанные сегменты сохранены: при повторном старте загрузка продолжится.")
				}
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
	if !j.only {
		if err := u.download(ctx, j); err != nil {
			return err
		}
	}
	if j.subs != nil {
		return u.makeSubs(ctx, j)
	}
	return nil
}

func (u *ui) download(ctx context.Context, j job) error {
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

// makeSubs runs Whisper on the whole merged video (not per segment, so phrases
// are not cut at segment borders) and embeds the result as a subtitle track.
func (u *ui) makeSubs(ctx context.Context, j job) error {
	fyne.Do(func() {
		u.log("Распознаю речь (Whisper), это может занять время…")
		u.status.SetText("Распознавание речи: 0%")
		u.barInf.Stop()
		u.barInf.Hide()
		u.bar.Max = 100
		u.bar.SetValue(0)
		u.bar.Show()
	})
	o := *j.subs
	o.OnProgress = func(p int) {
		fyne.Do(func() {
			u.status.SetText(fmt.Sprintf("Распознавание речи: %d%%", p))
			u.bar.SetValue(float64(p))
		})
	}
	srt, err := subs.Generate(ctx, j.out, o)
	if err != nil {
		return err
	}
	fyne.Do(func() {
		u.log("Субтитры: " + srt)
		u.status.SetText("Вшиваю субтитры в mp4…")
	})
	if err := subs.Embed(ctx, j.ffmpeg, j.out, srt, subs.SubLang(o)); err != nil {
		return err
	}
	fyne.Do(func() { u.log("Субтитры вшиты в mp4 (в VLC: клавиша V)") })
	return nil
}

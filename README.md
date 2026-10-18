# TS Downloader

Скачивает пронумерованные `.ts`-сегменты видео и склеивает их в один `.mp4` (через ffmpeg, без перекодирования).

## Как пользоваться
1. **Base URL**: `https://example.com/video/`
2. **Имя файла**: шаблон с `{}` на месте номера, например `seg_{}.ts` или `720p_{}_v2.ts`
3. **Номера**: «с» обязателен. «по» можно оставить пустым: тогда загрузка идёт до первого ответа 404. «цифр» задаёт ведущие нули (`5` → `00042`).
4. **Заголовки** (необязательно): по одному в строке, `Referer: https://site/`. Нужны, если сервер отвечает 403.
5. Нажмите **Старт**. В поле «Ссылка» сразу видно, какой URL получится.

Сегменты сохраняются в `<папка>/<имя>_parts/`. Если загрузку остановить или она оборвётся, при повторном старте уже скачанные сегменты пропускаются.

Нужен установленный `ffmpeg`: из PATH или по пути, указанному в поле «ffmpeg».

## Субтитры (Whisper)
Включите галочку «Сгенерировать субтитры»: после склейки тула распознает речь по всему видео (не по отдельным сегментам, чтобы фразы не резались на стыках). Рядом с mp4 появится `.srt`, и он же будет вшит в mp4 отдельной дорожкой (в VLC включается клавишей V). Кнопка «Субтитры к готовому mp4» делает то же для уже скачанного файла (папка + имя файла).

- **Язык речи**: `auto`, `en` или `ru`. «Перевести на английский» делает английские субтитры из речи на любом языке. На русский Whisper не переводит.
- Нужен [whisper.cpp](https://github.com/ggml-org/whisper.cpp) (`whisper-cli`): на Arch `sudo pacman -S whisper-cpp`, на macOS `brew install whisper-cpp`.
- Нужна модель `ggml-*.bin` с [Hugging Face](https://huggingface.co/ggerganov/whisper.cpp/tree/main):
  - `ggml-large-v3-turbo.bin` (~1.6 ГБ): лучший выбор для распознавания, но переводит плохо;
  - `ggml-large-v3.bin` или `ggml-medium.bin`: если нужен перевод на английский;
  - `ggml-small.bin`: быстрее, но ошибается чаще.

## Сборка
Нужен Go ≥ 1.24 и C-компилятор (Fyne использует cgo/OpenGL).

```sh
go build -o ts-downloader .
go test ./...
```

Под Linux нужны dev-пакеты X11/GL: на Debian/Ubuntu это `libgl1-mesa-dev xorg-dev`, на Arch они обычно уже стоят.

### Под все платформы (тот же код)
Через [fyne-cross](https://github.com/fyne-io/fyne-cross) (нужен Docker):

```sh
go install github.com/fyne-io/fyne-cross@latest
fyne-cross linux   -arch=amd64,arm64
fyne-cross windows -arch=amd64
fyne-cross darwin  -arch=amd64,arm64   # нужен macOS SDK, проще собрать на маке
```

Результаты появятся в `fyne-cross/dist/`. Другой вариант: на целевой ОС выполнить `go build` или `go run fyne.io/tools/cmd/fyne@latest package`.

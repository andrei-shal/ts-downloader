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

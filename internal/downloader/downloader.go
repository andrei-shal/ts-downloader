// Package downloader fetches numbered .ts segments in parallel.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ts-downloader/internal/pattern"
)

// NoEnd means "download until the server returns 404/410".
const NoEnd = -1

type Config struct {
	Base     string
	Template string
	Pad      int
	Start    int
	End      int // inclusive; NoEnd for auto-stop
	Workers  int
	Headers  http.Header
	PartsDir string

	Retries    int           // extra attempts on network errors, 5xx and 429; default 3
	RetryDelay time.Duration // first backoff delay, doubles each retry; default 1s
	Client     *http.Client  // optional

	// Callbacks may be called from several goroutines at once.
	OnSegment func(done int) // a segment is ready (downloaded or already on disk)
	OnLog     func(msg string)
}

// Result lists the downloaded segment files in playback order.
type Result struct {
	Files []string
}

var errNotFound = errors.New("not found")

// errPermanent marks errors that retrying will not fix (e.g. 403).
type errPermanent struct{ error }

func Run(ctx context.Context, cfg Config) (Result, error) {
	if err := pattern.Validate(cfg.Template); err != nil {
		return Result{}, err
	}
	if cfg.End != NoEnd && cfg.End < cfg.Start {
		return Result{}, errors.New("конечный номер меньше начального")
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	if cfg.OnSegment == nil {
		cfg.OnSegment = func(int) {}
	}
	if cfg.OnLog == nil {
		cfg.OnLog = func(string) {}
	}
	if err := os.MkdirAll(cfg.PartsDir, 0o755); err != nil {
		return Result{}, err
	}

	// Segments with numbers >= stopAt are not needed.
	var stopAt atomic.Int64
	if cfg.End == NoEnd {
		stopAt.Store(math.MaxInt64)
	} else {
		stopAt.Store(int64(cfg.End) + 1)
	}
	lowerStop := func(n int) {
		for {
			cur := stopAt.Load()
			if int64(n) >= cur || stopAt.CompareAndSwap(cur, int64(n)) {
				return
			}
		}
	}

	var (
		mu       sync.Mutex
		failed   = map[int]error{}
		done     atomic.Int64
		jobs     = make(chan int)
		wg       sync.WaitGroup
		notFound = map[int]bool{} // only used when End is set
	)

	// A segment that fails after all retries aborts the whole run;
	// otherwise auto-stop mode would keep generating numbers forever (e.g. on 403).
	runCtx, abort := context.WithCancel(ctx)
	defer abort()

	go func() {
		defer close(jobs)
		for n := cfg.Start; int64(n) < stopAt.Load(); n++ {
			select {
			case jobs <- n:
			case <-runCtx.Done():
				return
			}
		}
	}()

	for range cfg.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				if int64(n) >= stopAt.Load() || runCtx.Err() != nil {
					continue
				}
				err := fetchSegment(runCtx, cfg, n)
				switch {
				case err == nil:
					cfg.OnSegment(int(done.Add(1)))
				case errors.Is(err, errNotFound) && cfg.End == NoEnd:
					lowerStop(n)
				case errors.Is(err, errNotFound):
					mu.Lock()
					notFound[n] = true
					mu.Unlock()
				case runCtx.Err() != nil:
				default:
					cfg.OnLog(fmt.Sprintf("сегмент %d: %v", n, err))
					mu.Lock()
					failed[n] = err
					mu.Unlock()
					abort()
				}
			}
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	stop := int(stopAt.Load())
	if len(notFound) > 0 {
		return Result{}, fmt.Errorf("сервер вернул 404 для сегментов: %s", listNums(keys(notFound), stop))
	}
	var bad []int
	for n := range failed {
		if n < stop {
			bad = append(bad, n)
		}
	}
	if len(bad) > 0 {
		return Result{}, fmt.Errorf("не удалось скачать сегменты: %s", listNums(bad, stop))
	}
	if stop <= cfg.Start {
		return Result{}, fmt.Errorf("первый сегмент (%d) не найден на сервере, проверьте ссылку", cfg.Start)
	}

	files := make([]string, 0, stop-cfg.Start)
	for n := cfg.Start; n < stop; n++ {
		files = append(files, segmentPath(cfg.PartsDir, n))
	}
	return Result{Files: files}, nil
}

func segmentPath(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("%010d.ts", n))
}

func fetchSegment(ctx context.Context, cfg Config, n int) error {
	path := segmentPath(cfg.PartsDir, n)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return nil // already downloaded earlier
	}
	url, err := pattern.BuildURL(cfg.Base, cfg.Template, n, cfg.Pad)
	if err != nil {
		return err
	}

	delay := cfg.RetryDelay
	for attempt := 0; ; attempt++ {
		err = download(ctx, cfg, url, path)
		var perm errPermanent
		if err == nil || errors.Is(err, errNotFound) || errors.As(err, &perm) ||
			ctx.Err() != nil || attempt >= cfg.Retries {
			return err
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		delay *= 2
	}
}

func download(ctx context.Context, cfg Config, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errPermanent{err}
	}
	for k, vs := range cfg.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return errNotFound
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return errPermanent{fmt.Errorf("HTTP %d (возможно, нужны заголовки Referer/User-Agent/Cookie)", resp.StatusCode)}
	default:
		return errPermanent{fmt.Errorf("HTTP %d", resp.StatusCode)}
	}

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return errPermanent{err}
	}
	written, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && written == 0 {
		err = errors.New("пустой ответ")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// ParseHeaders turns "Name: value" lines into http.Header, skipping blank lines.
func ParseHeaders(text string) (http.Header, error) {
	h := http.Header{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("неверный заголовок: %q (нужно «Имя: значение»)", line)
		}
		h.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	return h, nil
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// listNums prints up to 10 sorted numbers below hi.
func listNums(nums []int, hi int) string {
	sort.Ints(nums)
	var parts []string
	for _, n := range nums {
		if n >= hi {
			continue
		}
		if len(parts) == 10 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprint(n))
	}
	return strings.Join(parts, ", ")
}

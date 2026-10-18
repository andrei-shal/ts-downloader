package downloader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// server serves /seg_<n>.ts for n in [1, last], 404 otherwise.
// failOnce makes the first request for that number return 500.
func server(t *testing.T, last int, failOnce map[int]bool) (*httptest.Server, *atomic.Int64) {
	var hits atomic.Int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Referer") != "https://example.com/" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/seg_"), ".ts")
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > last {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		fail := failOnce[n]
		delete(failOnce, n)
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "segment-%d", n)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func cfg(t *testing.T, base string, dir string) Config {
	h, err := ParseHeaders("Referer: https://example.com/\n\n")
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Base: base, Template: "seg_{}.ts", Start: 1, End: NoEnd,
		Workers: 4, Headers: h, PartsDir: dir, RetryDelay: time.Millisecond,
	}
}

func TestAutoStopAndResume(t *testing.T) {
	srv, hits := server(t, 20, map[int]bool{5: true})
	dir := t.TempDir()

	res, err := Run(context.Background(), cfg(t, srv.URL, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 20 {
		t.Fatalf("got %d files, want 20", len(res.Files))
	}
	for i, f := range res.Files {
		b, err := os.ReadFile(f)
		if err != nil || string(b) != fmt.Sprintf("segment-%d", i+1) {
			t.Fatalf("file %s: %q %v", f, b, err)
		}
	}

	// Second run must not re-download existing segments: only the 404 probes hit the server.
	hits.Store(0)
	res, err = Run(context.Background(), cfg(t, srv.URL, dir))
	if err != nil || len(res.Files) != 20 {
		t.Fatalf("resume: %d files, %v", len(res.Files), err)
	}
	if h := hits.Load(); h > 4 {
		t.Fatalf("resume made %d requests, want <= workers", h)
	}
}

func TestFixedRange(t *testing.T) {
	srv, _ := server(t, 20, nil)
	c := cfg(t, srv.URL, t.TempDir())
	c.Start, c.End = 3, 7
	res, err := Run(context.Background(), c)
	if err != nil || len(res.Files) != 5 {
		t.Fatalf("got %d files, %v", len(res.Files), err)
	}

	c.PartsDir, c.End = t.TempDir(), 25
	if _, err := Run(context.Background(), c); err == nil {
		t.Fatal("expected error for 404 inside fixed range")
	}
}

func TestErrors(t *testing.T) {
	srv, _ := server(t, 20, nil)

	c := cfg(t, srv.URL, t.TempDir())
	c.Start = 100
	if _, err := Run(context.Background(), c); err == nil {
		t.Fatal("expected error when first segment is missing")
	}

	c = cfg(t, srv.URL, t.TempDir())
	c.Headers = nil // server answers 403
	if _, err := Run(context.Background(), c); err == nil || !strings.Contains(err.Error(), "не удалось") {
		t.Fatalf("expected download error, got %v", err)
	}
}

func TestCancel(t *testing.T) {
	srv, _ := server(t, 1_000_000, nil)
	ctx, cancel := context.WithCancel(context.Background())
	c := cfg(t, srv.URL, t.TempDir())
	c.OnSegment = func(done int) {
		if done == 10 {
			cancel()
		}
	}
	if _, err := Run(ctx, c); err != context.Canceled {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

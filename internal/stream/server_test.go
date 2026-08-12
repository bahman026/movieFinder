package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// makeContent returns deterministic bytes of length n.
func makeContent(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('A' + i%26)
	}
	return b
}

func TestStreamSavesFullFileFromOneConnection(t *testing.T) {
	content := makeContent(500_000)
	var upstreamHits int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamHits, 1)
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		// Dribble it out so the download is observably progressive.
		for i := 0; i < len(content); i += 50_000 {
			end := i + 50_000
			if end > len(content) {
				end = len(content)
			}
			w.Write(content[i:end])
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	save := filepath.Join(t.TempDir(), "movie.mkv")
	s := New(upstream.URL, save)
	local, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// The player streams the whole thing from localhost.
	resp, err := http.Get(local)
	if err != nil {
		t.Fatalf("GET local: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(got) != len(content) {
		t.Fatalf("streamed %d bytes, want %d", len(got), len(content))
	}
	if string(got) != string(content) {
		t.Fatal("streamed content does not match source")
	}

	// Wait for the save to finish, then confirm the saved file is complete.
	waitDone(t, s)
	saved, err := os.ReadFile(save)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(content) {
		t.Fatalf("saved file is %d bytes, want %d", len(saved), len(content))
	}

	// Exactly one upstream connection was used, regardless of the player read.
	if got := atomic.LoadInt32(&upstreamHits); got != 1 {
		t.Errorf("upstream hit %d times, want 1 — only one internet connection may be used", got)
	}
}

func TestStreamServesRangeRequests(t *testing.T) {
	content := makeContent(200_000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.Write(content)
	}))
	defer upstream.Close()

	save := filepath.Join(t.TempDir(), "movie.mp4")
	s := New(upstream.URL, save)
	local, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	waitDone(t, s)

	req, _ := http.NewRequest(http.MethodGet, local, nil)
	req.Header.Set("Range", "bytes=100000-100099")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 100000-100099/200000" {
		t.Errorf("Content-Range = %q, want bytes 100000-100099/200000", cr)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(content[100000:100100]) {
		t.Errorf("range body did not match the requested slice")
	}
}

// A range that seeks ahead of the current download point must block until those
// bytes arrive, not fail — this is what lets playback seek without a second
// connection.
func TestRangeAheadOfDownloadBlocksThenServes(t *testing.T) {
	content := makeContent(300_000)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write(content[:50_000])
		w.(http.Flusher).Flush()
		<-release // hold the rest back
		w.Write(content[50_000:])
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	save := filepath.Join(t.TempDir(), "movie.mkv")
	s := New(upstream.URL, save)
	local, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Request bytes well beyond the 50k downloaded so far.
	req, _ := http.NewRequest(http.MethodGet, local, nil)
	req.Header.Set("Range", "bytes=250000-250099")

	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- result{body: b}
	}()

	// The request should still be blocked (bytes not downloaded yet).
	select {
	case <-done:
		t.Fatal("range request returned before its bytes were downloaded")
	case <-time.After(150 * time.Millisecond):
	}

	close(release) // let the rest download
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("range request: %v", r.err)
		}
		if string(r.body) != string(content[250000:250100]) {
			t.Error("ahead-of-download range served wrong bytes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("range request never completed after bytes arrived")
	}
}

func TestPauseStopsProgressAndResumeFinishes(t *testing.T) {
	content := makeContent(400_000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		// Dribble slowly so a pause lands mid-download.
		for i := 0; i < len(content); i += 20_000 {
			end := i + 20_000
			if end > len(content) {
				end = len(content)
			}
			w.Write(content[i:end])
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	save := filepath.Join(t.TempDir(), "movie.mkv")
	s := New(upstream.URL, save)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Let some data arrive, then pause.
	time.Sleep(60 * time.Millisecond)
	s.Pause()
	if !s.IsPaused() {
		t.Fatal("IsPaused = false after Pause")
	}

	// Pause takes effect after the current in-flight read finishes, so let it
	// settle before sampling the baseline.
	time.Sleep(80 * time.Millisecond)
	d1, _, _, _ := s.Progress()

	// From here, while paused, progress must not advance at all.
	time.Sleep(150 * time.Millisecond)
	d2, _, done, _ := s.Progress()
	if done {
		t.Fatal("download finished while paused")
	}
	if d2 != d1 {
		t.Errorf("progress advanced while paused: %d -> %d", d1, d2)
	}

	// Resume and let it finish.
	s.Resume()
	waitDone(t, s)
	saved, _ := os.ReadFile(save)
	if len(saved) != len(content) {
		t.Fatalf("after resume, saved %d bytes, want %d", len(saved), len(content))
	}
}

func TestStopCancelsAndWakesReaders(t *testing.T) {
	content := makeContent(300_000)
	hold := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write(content[:20_000])
		w.(http.Flusher).Flush()
		<-hold // never send the rest
	}))
	defer upstream.Close()
	defer close(hold)

	save := filepath.Join(t.TempDir(), "movie.mkv")
	s := New(upstream.URL, save)
	local, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A reader waiting for bytes beyond what will ever arrive must be released
	// by Stop rather than hang.
	req, _ := http.NewRequest(http.MethodGet, local, nil)
	req.Header.Set("Range", "bytes=250000-250099")
	done := make(chan struct{})
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	s.Stop()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reader was not released by Stop")
	}
	if !s.Stopped() {
		t.Error("Stopped() = false after Stop")
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		header           string
		total            int64
		wStart, wEnd     int64
		wOK              bool
	}{
		{"", 1000, 0, 999, false},
		{"bytes=0-", 1000, 0, 999, true},
		{"bytes=100-199", 1000, 100, 199, true},
		{"bytes=-200", 1000, 800, 999, true}, // suffix range
		{"bytes=500-", 1000, 500, 999, true},
	}
	for _, c := range cases {
		s, e, ok := parseRange(c.header, c.total)
		if s != c.wStart || e != c.wEnd || ok != c.wOK {
			t.Errorf("parseRange(%q,%d) = (%d,%d,%v), want (%d,%d,%v)",
				c.header, c.total, s, e, ok, c.wStart, c.wEnd, c.wOK)
		}
	}
}

func waitDone(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, done, err := s.Progress(); done {
			if err != nil {
				t.Fatalf("download failed: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download did not finish in time")
}

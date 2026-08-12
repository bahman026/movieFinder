package stream

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"moviefinder/internal/delfan"
)

// Opt-in end-to-end test of the whole pipeline against the real servers:
// Delfan sign-in + search + details + link resolution, then the tee download.
// No player is launched — it just confirms bytes flow from localhost while the
// save file fills, over a single upstream connection.
//
//	STREAM_LIVE=1 go test ./internal/stream/ -run TestLiveTee -v
func TestLiveTee(t *testing.T) {
	if os.Getenv("STREAM_LIVE") != "1" {
		t.Skip("set STREAM_LIVE=1 to run the live tee test against real servers")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	d := delfan.New("", "")
	results, err := d.Search(ctx, "joker", 1)
	if err != nil || len(results) == 0 {
		t.Fatalf("delfan search: %v (n=%d)", err, len(results))
	}
	detail, err := d.Details(ctx, results[0].ID)
	if err != nil || len(detail.DownloadLinks) == 0 {
		t.Fatalf("delfan details: %v (links=%d)", err, len(detail.DownloadLinks))
	}
	realURL, err := d.ResolveDownloadURL(ctx, detail.DownloadLinks[0])
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("streaming %q from %s", detail.Title, realURL)

	save := filepath.Join(t.TempDir(), "live.mkv")
	s := New(realURL, save)
	local, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("stream Start: %v", err)
	}
	defer s.Stop()

	// Read the first ~1 MB from localhost, like a player buffering the start.
	req, _ := http.NewRequest(http.MethodGet, local, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET localhost: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read localhost stream: %v", err)
	}
	if len(got) < 1<<20 {
		t.Fatalf("only %d bytes streamed from localhost, want >= 1 MB", len(got))
	}

	downloaded, total, _, derr := s.Progress()
	if derr != nil {
		t.Fatalf("download error: %v", derr)
	}
	t.Logf("OK: served %d bytes from localhost; downloaded=%d total=%d", len(got), downloaded, total)
	if info, err := os.Stat(save); err == nil {
		t.Logf("save file is growing: %d bytes on disk", info.Size())
	}
}

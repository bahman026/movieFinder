package mysubs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in tests against the real site. Scraping breaks silently when a page is
// restyled, and only the live pages can tell you that:
//
//	MYSUBS_LIVE=1 go test ./internal/mysubs/ -run TestLive -v
func TestLiveMovieSearchAndDownload(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := New("")
	subs, err := c.Search(ctx, Query{Title: "Inception", Year: "2010", Language: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("no English subtitles for Inception — the page layout probably changed")
	}
	t.Logf("%d result(s); first: %q (%d downloads)", len(subs), subs[0].Release, subs[0].DownloadCount)

	data, name, err := c.Download(ctx, subs[0].ID)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(data) < 100 {
		t.Fatalf("downloaded %d bytes, that is not a subtitle", len(data))
	}
	if !strings.Contains(strings.ToLower(name), ".srt") {
		t.Logf("note: downloaded file is named %q", name)
	}
	t.Logf("downloaded %q, %d bytes", name, len(data))
}

func TestLivePersianSearch(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	subs, err := New("").Search(ctx, Query{Title: "Inception", Year: "2010", Language: "fa"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("no Persian subtitles for Inception — check the language aliases")
	}
	for _, sub := range subs {
		t.Logf("%s — %s (%d downloads)", sub.Language, sub.Release, sub.DownloadCount)
	}
}

func TestLiveEpisodeSearch(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	subs, err := New("").Search(ctx, Query{Title: "Breaking Bad", Season: 1, Episode: 1, Language: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("no English subtitles for Breaking Bad S01E01")
	}
	t.Logf("%d result(s); first: %q by %s %s", len(subs), subs[0].Release, subs[0].Uploader, subs[0].Age)
}

func skipUnlessLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MYSUBS_LIVE") != "1" {
		t.Skip("set MYSUBS_LIVE=1 to test against the real my-subs.co")
	}
}

package api

import (
	"context"
	"os"
	"testing"
	"time"

	"moviefinder/internal/config"
)

// These tests hit the real servers, so they are opt-in:
//
//	go test ./internal/api/ -run TestLive -v      (with MOVIEFINDER_LIVE=1)
//
// They exist to catch the API changing shape underneath the client, which no
// amount of local mocking will tell you.
func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("MOVIEFINDER_LIVE") != "1" {
		t.Skip("set MOVIEFINDER_LIVE=1 to run tests against the real servers")
	}
	return New(config.Default())
}

func TestLiveMoviesFirstPage(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	movies, err := client.Movies(ctx, 1)
	if err != nil {
		t.Fatalf("Movies: %v", err)
	}
	if len(movies) == 0 {
		t.Fatal("page 1 came back empty")
	}
	first := movies[0]
	if first.ID == "" || first.Title == "" {
		t.Errorf("first entry is missing id or title: %+v", first)
	}
	t.Logf("host=%s  %d titles, first=%q (%s, %s)",
		client.ActiveHost(), len(movies), first.Title, first.Year(), first.KindLabel())
}

func TestLiveSearch(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := client.Search(ctx, "matrix")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("search for \"matrix\" returned nothing")
	}
	t.Logf("%d result(s), first=%q", len(results), results[0].Title)
}

func TestLiveDetailsHaveDownloadLinks(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	detail, err := client.Details(ctx, "movie", "15497")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if detail.Title == "" {
		t.Error("detail has no title")
	}
	if len(detail.DownloadLinks) == 0 {
		t.Error("detail has no download links")
	}
	for i, link := range detail.DownloadLinks {
		if link.URL == "" {
			t.Errorf("download link %d has no URL", i)
		}
	}
	t.Logf("%q — %d download link(s), %d genre(s), %d cast; first link: %s",
		detail.Title, len(detail.DownloadLinks), len(detail.Genre), len(detail.Cast),
		detail.DownloadLinks[0].Describe())
}

// Every configured host must serve the API, or failover is not a real fallback.
func TestLiveEachHostServesTheAPI(t *testing.T) {
	if os.Getenv("MOVIEFINDER_LIVE") != "1" {
		t.Skip("set MOVIEFINDER_LIVE=1 to run tests against the real servers")
	}
	for _, host := range config.Default().CleanHosts() {
		t.Run(host, func(t *testing.T) {
			cfg := config.Default()
			cfg.Hosts = []string{host}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			movies, err := New(cfg).Movies(ctx, 1)
			if err != nil {
				t.Fatalf("%s: %v", host, err)
			}
			t.Logf("%s served %d titles", host, len(movies))
		})
	}
}

package delfan

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in tests against the real Delfan servers:
//
//	DELFAN_LIVE=1 go test ./internal/delfan/ -run TestLive -v
//
// They exercise the actual signing and rolling-nonce chain end to end, which no
// mock can prove stays in sync with the live service.
func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("DELFAN_LIVE") != "1" {
		t.Skip("set DELFAN_LIVE=1 to run tests against the real Delfan servers")
	}
	return New("", "")
}

func TestLiveSearchAndDetails(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	results, err := c.Search(ctx, "joker", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("search for joker returned nothing")
	}
	t.Logf("search returned %d titles; first: %q (%s) imdb %s",
		len(results), results[0].Title, results[0].Year, results[0].IMDB)

	// Details on the first result must return download links.
	d, err := c.Details(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if len(d.DownloadLinks) == 0 {
		t.Fatalf("no download links for %q", d.Title)
	}
	t.Logf("details %q (orig %q): %d link(s); first: %s",
		d.Title, d.OriginalTitle, len(d.DownloadLinks), d.DownloadLinks[0].Describe())

	// Resolving the first link must yield a real file URL (302 target).
	url, err := c.ResolveDownloadURL(ctx, d.DownloadLinks[0])
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	t.Logf("resolved download URL: %s", url)
}

func TestLiveHome(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	items, err := c.Home(ctx)
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("home page returned no titles")
	}
	t.Logf("home returned %d titles; first: %q (%s)", len(items), items[0].Title, items[0].Year)
}

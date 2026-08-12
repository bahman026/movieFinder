package opensubtitles

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in test that the built-in DefaultAPIKey actually works against the live
// service:
//
//	OPENSUBTITLES_LIVE=1 go test ./internal/opensubtitles/ -run TestLive -v
func TestLiveDefaultKeySearches(t *testing.T) {
	if os.Getenv("OPENSUBTITLES_LIVE") != "1" {
		t.Skip("set OPENSUBTITLES_LIVE=1 to test the built-in key against the real API")
	}
	if DefaultAPIKey == "" {
		t.Skip("no built-in key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	subs, err := New(ResolveKey("")).Search(ctx, "the matrix", "133093", "1999", "en")
	if err != nil {
		t.Fatalf("Search with built-in key: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("built-in key returned no subtitles for The Matrix")
	}
	t.Logf("built-in key OK: %d English subtitle(s); first: %q", len(subs), subs[0].Release)
}

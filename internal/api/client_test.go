package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"moviefinder/internal/config"
)

// sampleListing mirrors the real get_movies payload: a bare array of strings.
const sampleListing = `[
  {"videos_id":"26039","title":"A Friend of Dorothy","imdb_rating":"7.2","slug":"a-friend-of-dorothy",
   "release":"2025","is_tvseries":"0","runtime":"22 Min","writer":"local title","video_quality":"sub"}
]`

func testConfig(hosts ...string) config.Config {
	cfg := config.Default()
	cfg.Hosts = hosts
	cfg.TimeoutSeconds = 5
	return cfg
}

func TestMoviesDecodesBareArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want 2", got)
		}
		if got := r.URL.Query().Get("api_secret_key"); got == "" {
			t.Error("api_secret_key was not sent")
		}
		if !strings.HasSuffix(r.URL.Path, "/get_movies") {
			t.Errorf("path = %q, want it to end in /get_movies", r.URL.Path)
		}
		w.Write([]byte(sampleListing))
	}))
	defer server.Close()

	movies, err := New(testConfig(server.URL)).Movies(context.Background(), 2)
	if err != nil {
		t.Fatalf("Movies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies, want 1", len(movies))
	}
	if movies[0].Year() != "2025" {
		t.Errorf("Year() = %q, want 2025", movies[0].Year())
	}
	if movies[0].Kind() != "movie" {
		t.Errorf("Kind() = %q, want movie", movies[0].Kind())
	}
}

func TestSearchFlattensCategories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "the matrix" {
			t.Errorf("q = %q, want %q", got, "the matrix")
		}
		w.Write([]byte(`{"movie":[{"videos_id":"1","title":"M","is_tvseries":"0"}],
		                "tvseries":[{"videos_id":"2","title":"S","is_tvseries":"1"}],
		                "tv_channels":[]}`))
	}))
	defer server.Close()

	results, err := New(testConfig(server.URL)).Search(context.Background(), "the matrix")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Films come first, then series.
	if results[0].Kind() != "movie" || results[1].Kind() != "tvseries" {
		t.Errorf("kinds = %q, %q; want movie, tvseries", results[0].Kind(), results[1].Kind())
	}
}

// The API reports failures in a 200 response body, so the status code alone
// never reveals them.
func TestErrorEnvelopeIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error","message":"Search string is empty."}`))
	}))
	defer server.Close()

	_, err := New(testConfig(server.URL)).Search(context.Background(), "x")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "Search string is empty") {
		t.Errorf("error = %q, want it to carry the API message", err)
	}
}

func TestFailsOverToNextHostAndPinsIt(t *testing.T) {
	var deadHits int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&deadHits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleListing))
	}))
	defer live.Close()

	client := New(testConfig(dead.URL, live.URL))

	if _, err := client.Movies(context.Background(), 1); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if client.ActiveHost() != live.URL {
		t.Errorf("ActiveHost = %q, want the live host %q", client.ActiveHost(), live.URL)
	}

	// The dead host must not be retried on every later call.
	if _, err := client.Movies(context.Background(), 1); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := atomic.LoadInt32(&deadHits); got != 1 {
		t.Errorf("dead host was hit %d times, want 1 — the working host should stay pinned", got)
	}
}

func TestAllHostsDownReportsEachFailure(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer other.Close()

	_, err := New(testConfig(down.URL, other.URL)).Movies(context.Background(), 1)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	for _, host := range []string{down.URL, other.URL} {
		if !strings.Contains(err.Error(), host) {
			t.Errorf("error %q does not mention %q", err, host)
		}
	}
}

// The real API fills `resolution` with a decorative glyph and gives
// `file_size` as bare megabytes, often null.
func TestDownloadLinkDescribe(t *testing.T) {
	cases := []struct {
		name string
		link DownloadLink
		want string
	}{
		{
			name: "glyph resolution is dropped, size converted to GB",
			link: DownloadLink{Label: "1080P", Resolution: "⇩", FileSize: "1603",
				URL: "http://dl14.example.net/x/The.Movie.1080p.mkv?md5=abc&expires=1"},
			want: "1080P · 1.6 GB · mkv",
		},
		{
			name: "null size is omitted",
			link: DownloadLink{Label: "Trailer", Resolution: "⇩", FileSize: "",
				URL: "http://dl4.example.net/x/-Trailer-.mp4?md5=abc"},
			want: "Trailer · mp4",
		},
		{
			name: "megabytes stay megabytes",
			link: DownloadLink{Label: "480P", FileSize: "453", URL: "http://x/a.mkv"},
			want: "480P · 453 MB · mkv",
		},
		{
			name: "a meaningful resolution is kept",
			link: DownloadLink{Label: "HD", Resolution: "1920x800", URL: "http://x/a.mp4"},
			want: "HD · 1920x800 · mp4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.link.Describe(); got != tc.want {
				t.Errorf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The query string on a signed URL must not be mistaken for an extension.
func TestDownloadLinkExt(t *testing.T) {
	cases := map[string]string{
		"http://dl.example.net/a/The.Movie.1080p.mkv?md5=abc&expires=123": ".mkv",
		"http://dl.example.net/a/-Trailer-.mp4?md5=abc":                   ".mp4",
		"http://dl.example.net/a/nofile":                                  ".mp4",
		"":                                                                ".mp4",
	}
	for url, want := range cases {
		if got := (DownloadLink{URL: url}).Ext(); got != want {
			t.Errorf("Ext(%q) = %q, want %q", url, got, want)
		}
	}
}

// A 4xx would fail the same way everywhere, so it must not burn the mirrors.
func TestClientErrorDoesNotFailOver(t *testing.T) {
	var secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		w.Write([]byte(sampleListing))
	}))
	defer second.Close()

	if _, err := New(testConfig(first.URL, second.URL)).Movies(context.Background(), 1); err == nil {
		t.Fatal("want an error, got nil")
	}
	if got := atomic.LoadInt32(&secondHits); got != 0 {
		t.Errorf("second host was hit %d times, want 0", got)
	}
}

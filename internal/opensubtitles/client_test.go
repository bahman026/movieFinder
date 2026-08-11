package opensubtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// useTestServer points the client at server for the duration of the test.
func useTestServer(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := currentBaseURL
	currentBaseURL = server.URL + "/api/v1"
	t.Cleanup(func() { currentBaseURL = original })
}

func TestMissingAPIKeyFailsFastWithNoRequest(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()
	useTestServer(t, server)

	client := New("")
	if _, err := client.Search(context.Background(), "the matrix", "", "1999", "en"); err == nil {
		t.Fatal("want an error, got nil")
	} else if !strings.Contains(err.Error(), RegisterURL) {
		t.Errorf("error %q does not point at %s", err, RegisterURL)
	}
	if _, _, err := client.Download(context.Background(), 123); err == nil {
		t.Fatal("want an error, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("server was hit %d times, want 0 — a missing key must not reach the network", got)
	}
}

// The real API types feature_details.year as a JSON number, but this is
// exactly the kind of field that could plausibly ship as a string too, and it
// cannot be checked live without an account. flexInt must accept either.
func TestSearchToleratesYearAsNumberOrString(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"number", `{"data":[{"id":"1","attributes":{"language":"en","release":"X",
			"feature_details":{"movie_name":"X","year":1999},"files":[{"file_id":7,"file_name":"x.srt"}]}}]}`},
		{"string", `{"data":[{"id":"1","attributes":{"language":"en","release":"X",
			"feature_details":{"movie_name":"X","year":"1999"},"files":[{"file_id":7,"file_name":"x.srt"}]}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			useTestServer(t, server)

			client := New("key")
			subs, err := client.Search(context.Background(), "x", "tt123", "", "en")
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(subs) != 1 || subs[0].Year != "1999" {
				t.Fatalf("got %+v, want one result with Year=1999", subs)
			}
		})
	}
}

func TestSearchByIMDBIDFallsBackToTitleWhenEmpty(t *testing.T) {
	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.RawQuery)
		if strings.Contains(r.URL.RawQuery, "imdb_id") {
			w.Write([]byte(`{"data":[]}`)) // nothing indexed under this id
			return
		}
		w.Write([]byte(`{"data":[{"id":"1","attributes":{"language":"en","release":"X",
			"feature_details":{"movie_name":"X","year":2000},"files":[{"file_id":9,"file_name":"x.srt"}]}}]}`))
	}))
	defer server.Close()
	useTestServer(t, server)

	client := New("key")
	subs, err := client.Search(context.Background(), "X", "123", "2000", "en")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d results, want 1 from the title fallback", len(subs))
	}
	if len(gotQueries) != 2 {
		t.Fatalf("made %d requests, want 2 (imdb_id then title fallback): %v", len(gotQueries), gotQueries)
	}
	if !strings.Contains(gotQueries[0], "imdb_id") || !strings.Contains(gotQueries[1], "query") {
		t.Errorf("request order was %v, want imdb_id first then query", gotQueries)
	}
}

func TestSearchEntriesWithNoFilesAreSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":"1","attributes":{"language":"en","release":"no files","files":[]}},
			{"id":"2","attributes":{"language":"en","release":"has a file","files":[{"file_id":1,"file_name":"a.srt"}]}}
		]}`))
	}))
	defer server.Close()
	useTestServer(t, server)

	client := New("key")
	subs, err := client.Search(context.Background(), "x", "", "", "en")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 || subs[0].Release != "has a file" {
		t.Fatalf("got %+v, want only the entry with a file", subs)
	}
}

// The API reports problems (missing key, quota, ...) via a JSON message body
// on a non-2xx status, so that message must surface rather than a bare
// "403 Forbidden".
func TestErrorMessageSurfacesFromNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"You cannot consume this service"}`))
	}))
	defer server.Close()
	useTestServer(t, server)

	client := New("bad-key")
	_, err := client.Search(context.Background(), "x", "", "", "en")
	if err == nil || !strings.Contains(err.Error(), "You cannot consume this service") {
		t.Errorf("err = %v, want it to carry the API message", err)
	}
}

func TestDownloadFetchesLinkThenFile(t *testing.T) {
	const subtitleBody = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("download used %s, want POST", r.Method)
		}
		if r.Header.Get("Api-Key") == "" {
			t.Error("Api-Key header was not sent")
		}
		w.Write([]byte(`{"link":"` + linkTarget + `","file_name":"hello.srt"}`))
	})
	mux.HandleFunc("/files/hello.srt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(subtitleBody))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	useTestServer(t, server)
	linkTarget = server.URL + "/files/hello.srt"

	client := New("key")
	data, name, err := client.Download(context.Background(), 42)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if name != "hello.srt" {
		t.Errorf("file name = %q, want hello.srt", name)
	}
	if string(data) != subtitleBody {
		t.Errorf("data = %q, want %q", data, subtitleBody)
	}
}

// linkTarget is set per-test since the mock /download handler needs to embed
// the test server's own URL, which is only known once the server is started.
var linkTarget string

func TestDownloadWithoutLinkReturnsAPIMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":"Daily download limit reached"}`))
	}))
	defer server.Close()
	useTestServer(t, server)

	client := New("key")
	_, _, err := client.Download(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "Daily download limit reached") {
		t.Errorf("err = %v, want the API's quota message", err)
	}
}

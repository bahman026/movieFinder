package delfan

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func md5of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// A test server that mimics the real login/vitrin/filter_search chain and,
// crucially, validates the rolling nonce the way the real gated endpoints do:
// each response hands out fresh q1/q2, and the next gated request must carry
// an = MD5(prev_q1 + prev_q2 + 101).
type fakeServer struct {
	*httptest.Server
	step           int // drives the q1/q2 sequence
	lastQ1, lastQ2 int // the q1/q2 the gated handler last issued, for nonce validation
}

func newFakeServer(t *testing.T) *fakeServer {
	fs := &fakeServer{}
	// deterministic q1/q2 per response, avoiding rand in tests
	q := func() (int, int) {
		fs.step++
		return fs.step * 3, fs.step * 7
	}

	mux := http.NewServeMux()
	// login lives under users.php
	mux.HandleFunc("/app-plus/users.php", func(w http.ResponseWriter, r *http.Request) {
		q1, q2 := q()
		fmt.Fprintf(w, `{"night_mode":"US_TOKEN","q1":%d,"q2":%d,"infos":[{"auth":"AUTH_TOKEN","login":"F"}]}`, q1, q2)
	})
	// gated endpoints under vp1.php
	mux.HandleFunc("/app-plus/vp1.php", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		action := r.URL.Query().Get("action")

		// vitrin does not validate the nonce; everything else does. The handler
		// recomputes the expected nonce from the q1/q2 it last issued.
		if action != "vitrin" {
			if got := r.Form.Get("an"); got != nonce(fs.lastQ1, fs.lastQ2) {
				w.Write([]byte(`{"state_all":"F","msg":"1 - bad nonce"}`))
				return
			}
			// body must carry the session token
			if !strings.Contains(r.Form.Get("body"), "AUTH_TOKEN") {
				w.Write([]byte(`{"state_all":"F","msg":"2 - bad body"}`))
				return
			}
		}

		q1, q2 := q()
		fs.lastQ1, fs.lastQ2 = q1, q2
		switch {
		case action == "vitrin":
			fmt.Fprintf(w, `{"state_all":"T","q1":%d,"q2":%d,"NewMovie":[{"videos_id":"1","title":"A"}],"NewSerie":[{"videos_id":"2","title":"B"}]}`, q1, q2)
		case strings.HasPrefix(action, "filter_search"):
			fmt.Fprintf(w, `{"state_all":"T","q1":%d,"q2":%d,"all":[{"videos_id":"9","title":"Joker","year":"2019","imdb":"8.4"}]}`, q1, q2)
		case action == "detials":
			fmt.Fprintf(w, `{"state_all":"T","q1":%d,"q2":%d,"detiles":[{"id":9,"title":"Joker","year":"2019","imdb_rating":"8.4","is_movie":"1","description":"نام اصلی : Joker<br>x","download_link":[{"id":5,"name":"720","link":"%s/play?i=5","video_size":"800 MB"}]}]}`, q1, q2, fs.URL)
		default:
			w.Write([]byte(`{"state_all":"T","q1":1,"q2":1}`))
		}
	})
	// download resolver: 302 to the "real" file
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://files.example/movie.mkv?expire=1&hash=x")
		w.WriteHeader(http.StatusFound)
	})

	fs.Server = httptest.NewServer(mux)
	return fs
}

func clientFor(fs *fakeServer) *Client {
	c := New(fs.URL, fs.URL)
	// Use the test server's client but keep the real client's redirect policy,
	// so ResolveDownloadURL reads the 302 Location instead of following it.
	hc := fs.Client()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.http = hc
	return c
}

func TestNonceFormula(t *testing.T) {
	if got, want := nonce(29, 90), md5of("220"); got != want { // 29+90+101 = 220
		t.Errorf("nonce(29,90) = %q, want MD5(220) = %q", got, want)
	}
}

func TestBuildBodyStructure(t *testing.T) {
	body := buildBody("AUTHXY", DefaultAppVersion)
	// head(32) + auth + mid1 + mid2 + auth + tail + appversion + tail_md5(32)
	if len(body) < 64 {
		t.Fatalf("body too short: %d", len(body))
	}
	mid := body[32 : len(body)-32]
	want := "AUTHXY" + constMid1 + constMid2 + "AUTHXY" + constTail + DefaultAppVersion
	if mid != want {
		t.Errorf("body middle = %q, want %q", mid, want)
	}
	if _, err := hex.DecodeString(body[:32]); err != nil {
		t.Errorf("body head is not hex: %v", err)
	}
}

func TestSearchThreadsTheRollingNonce(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	c := clientFor(fs)

	// First search: login + vitrin seed happen inside, then the gated search
	// must carry the nonce from the vitrin response. If the chain is wrong the
	// fake server returns state_all F and Search errors.
	items, err := c.Search(context.Background(), "joker", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Joker" {
		t.Fatalf("got %+v, want one Joker result", items)
	}

	// A second gated call must advance the nonce again from the last response.
	if _, err := c.Search(context.Background(), "joker", 2); err != nil {
		t.Fatalf("second Search (nonce should have advanced): %v", err)
	}
}

func TestDetailsParsesLinksAndOriginalTitle(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	c := clientFor(fs)

	// prime the session
	if _, err := c.Search(context.Background(), "joker", 1); err != nil {
		t.Fatalf("prime: %v", err)
	}

	d, err := c.Details(context.Background(), "9")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if d.OriginalTitle != "Joker" {
		t.Errorf("OriginalTitle = %q, want Joker (parsed from description)", d.OriginalTitle)
	}
	if len(d.DownloadLinks) != 1 || d.DownloadLinks[0].Name != "720" {
		t.Fatalf("got links %+v, want one named 720", d.DownloadLinks)
	}

	realURL, err := c.ResolveDownloadURL(context.Background(), d.DownloadLinks[0])
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	if !strings.HasPrefix(realURL, "http://files.example/movie.mkv") {
		t.Errorf("resolved URL = %q, want the redirect target", realURL)
	}
}

func TestStaleNonceIsRejected(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	c := clientFor(fs)
	if err := func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.ensureSession(context.Background())
	}(); err != nil {
		t.Fatalf("ensureSession: %v", err)
	}

	// Deliberately corrupt the stored nonce, then a gated call must be rejected
	// by the server exactly as a replay would be.
	c.q1, c.q2 = 99999, 99999
	if _, err := c.Search(context.Background(), "joker", 1); err == nil {
		t.Fatal("want the server to reject a stale nonce, got success")
	}
}

func TestAsIntHandlesNumberAndString(t *testing.T) {
	if asInt(float64(42)) != 42 || asInt("42") != 42 || asInt(nil) != 0 {
		t.Error("asInt failed on number/string/nil")
	}
}

func TestOriginalTitleAndCleanDescription(t *testing.T) {
	desc := "نام اصلی : The Eyes<br>ژانر: معمایی\r<br>محصول: 2026"
	if got := originalTitle(desc); got != "The Eyes" {
		t.Errorf("originalTitle = %q, want The Eyes", got)
	}
	if strings.Contains(cleanDescription(desc), "<br>") || strings.Contains(cleanDescription(desc), "\r") {
		t.Error("cleanDescription left markup in place")
	}
}

// Guard: the encoded form actually carries the computed an on the wire.
func TestFormEncodesNonce(t *testing.T) {
	form := url.Values{}
	form.Set("an", nonce(3, 7))
	if !strings.Contains(form.Encode(), "an="+md5of(strconv.Itoa(3+7+101))) {
		t.Error("encoded form does not carry the expected nonce")
	}
}

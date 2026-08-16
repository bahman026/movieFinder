package mysubs

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fixtures below are trimmed copies of the real markup — the attribute
// quoting is inconsistent (double quotes on movie pages, single on episode
// pages) because the site is, and the parser has to survive both.

const searchPage = `<html><body>
<h3>Results For The Keyword : inception</h3>
<div class="panel"><div class="panel-heading"><b>Tv Shows</b></div><div class="list-group">
<a class='list-group-item' title='Inception The Series' href='/showlistsubtitles-99-inception-the-series' >Inception The Series</a>
</div></div>
<div class="panel"><div class="panel-heading"><b>Movies</b></div><div class="list-group">
<a class='list-group-item' title='Inception Reloaded' href='/film-versions-800-inception-reloaded-subtitles' >Inception Reloaded (2015)</a>
<a class='list-group-item' title='Inception' href='/film-versions-127-inception-subtitles' >Inception (2010)</a>
</div></div>
</body></html>`

const filmPage = `<html><body>
<h1 style='font-size: 27px;'><span class='glyphicon'></span> Movie Inception (2010) Subtitles</h1>
<h2>Download Inception Subtitles</h2>
<h3><span class="flag-icon flag-icon-gb" title="english"></span> english</h3>
<ul class="list-group">
<a rel='nofollow' href="/downloads/EN-LOW" class="list-group-item">
<div class="col-xs-12"><span class="flag-icon flag-icon-gb" title="english"></span> <small>
<strong>
Inception 2010 1080p BluRay x264 </strong>
<div class="pull-right"><b>40</b> <span class="glyphicon glyphicon-download-alt"></span></div>
</small></div></a>
<a rel='nofollow' href="/downloads/EN-HIGH" class="list-group-item">
<div class="col-xs-12"><span class="flag-icon flag-icon-gb" title="english"></span> <small>
<strong>
Inception 2010 720p BrRip x264 YIFY </strong>
<div class="pull-right"><b>1748</b> <span class="glyphicon glyphicon-download-alt"></span></div>
</small></div></a>
</ul>
<h3><span class="flag-icon flag-icon-ir" title="Farsi/Persian"></span> Farsi/Persian</h3>
<ul class="list-group">
<a rel='nofollow' href="/downloads/FA-1" class="list-group-item">
<div class="col-xs-12"><span class="flag-icon flag-icon-ir" title="Farsi/Persian"></span> <small>
<strong>
Inception.2010.720p.BluRay.Farsi </strong>
<div class="pull-right"><b>512</b> <span class="glyphicon glyphicon-download-alt"></span></div>
</small></div></a>
</ul>
</body></html>`

const episodePage = `<html><body>
<h1>Breaking Bad Season 2 Episode 3 Subtitles</h1>
<div style="background-color: #f5f5f5;">
<div class='version'><b>Version:</b> <i>DVDRip ORPHEUS</i> </div><br>
<div class='row'><div class='col-md-8'><div class='lang'><b>Language :</b> <span class="flag-icon flag-icon-gb" title="english"></span> <i>English</i></div> <br><b>Uploaded by :</b> <a href='/user-eva'>eva</a> 8 years ago <br><b>Downloads :</b> 4233</div><div class='col-md-4'><a rel='nofollow' href='/downloads/EP-EN'><button type='button' class='btn btn-info'> DOWNLOAD </button></a> </div></div><hr>
<div class='row'><div class='col-md-8'><div class='lang'><b>Language :</b> <span class="flag-icon flag-icon-ir" title="Persian"></span> <i>Persian</i></div> <br><b>Uploaded by :</b> <a href='/user-ali'>ali</a> 3 years ago <br><b>Downloads :</b> 77</div><div class='col-md-4'><a rel='nofollow' href='/downloads/EP-FA'><button type='button' class='btn btn-info'> DOWNLOAD </button></a> </div></div><hr>
</div>
</body></html>`

func gatePage(realPath string) string {
	return `<html><body><h2>Download</h2><script>(function(){var SECONDS=10;var REAL_URL="` +
		strings.ReplaceAll(realPath, "/", `\/`) +
		`";var $btn=document.getElementById('dlBtn');})()</script></body></html>`
}

// testServer serves the fixture pages and records the paths asked for.
func testServer(t *testing.T, extra map[string]http.HandlerFunc) (*Client, *[]string) {
	t.Helper()
	var asked []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		if h, ok := extra[r.URL.Path]; ok {
			h(w, r)
			return
		}
		switch {
		case r.URL.Path == "/search.php":
			w.Write([]byte(searchPage))
		case strings.HasPrefix(r.URL.Path, "/film-versions-127-"):
			w.Write([]byte(filmPage))
		case strings.HasPrefix(r.URL.Path, "/versions-"):
			w.Write([]byte(episodePage))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return New(srv.URL), &asked
}

func TestSearchPicksTheClosestMovieAndFiltersLanguage(t *testing.T) {
	c, asked := testServer(t, nil)

	subs, err := c.Search(context.Background(), Query{Title: "Inception", Year: "2010", Language: "fa"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// "Inception Reloaded" sorts first on the page and must not win over the
	// exact title.
	if len(*asked) < 2 || !strings.HasPrefix((*asked)[1], "/film-versions-127-") {
		t.Fatalf("wrong title page fetched, requests: %v", *asked)
	}
	if len(subs) != 1 {
		t.Fatalf("want 1 Persian subtitle, got %d: %+v", len(subs), subs)
	}
	got := subs[0]
	if got.ID != "/downloads/FA-1" || got.Language != "Farsi/Persian" {
		t.Errorf("wrong entry: %+v", got)
	}
	if got.DownloadCount != 512 {
		t.Errorf("DownloadCount = %d, want 512", got.DownloadCount)
	}
	if got.Title != "Inception" || got.Year != "2010" {
		t.Errorf("Title/Year = %q/%q, want Inception/2010", got.Title, got.Year)
	}
	if !strings.Contains(got.Release, "720p.BluRay.Farsi") {
		t.Errorf("Release = %q", got.Release)
	}
}

func TestSearchSortsByDownloadCount(t *testing.T) {
	c, _ := testServer(t, nil)

	subs, err := c.Search(context.Background(), Query{Title: "Inception", Language: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("want 2 English subtitles, got %d", len(subs))
	}
	if subs[0].ID != "/downloads/EN-HIGH" {
		t.Errorf("most downloaded should come first, got %q", subs[0].ID)
	}
}

// The site orders its episode URLs episode-then-season. Getting that backwards
// silently fetches a different episode, so it is asserted rather than assumed.
func TestEpisodeURLPutsEpisodeBeforeSeason(t *testing.T) {
	c, asked := testServer(t, nil)

	subs, err := c.Search(context.Background(), Query{Title: "Inception The Series", Season: 2, Episode: 3, Language: "fa"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := "/versions-99-3-2-inception-the-series-subtitles"
	if len(*asked) < 2 || (*asked)[1] != want {
		t.Fatalf("asked for %v, want %s second", *asked, want)
	}
	if len(subs) != 1 || subs[0].ID != "/downloads/EP-FA" {
		t.Fatalf("wrong entries: %+v", subs)
	}
	if subs[0].Uploader != "ali" || subs[0].Age != "3 years ago" {
		t.Errorf("uploader/age = %q/%q", subs[0].Uploader, subs[0].Age)
	}
	if subs[0].Release != "DVDRip ORPHEUS" {
		t.Errorf("Release = %q, want the version heading", subs[0].Release)
	}
}

func TestSeriesWithoutEpisodeAsksForOne(t *testing.T) {
	c, _ := testServer(t, map[string]http.HandlerFunc{
		"/search.php": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<html><body><div class="list-group">` +
				`<a class='list-group-item' title='Breaking Bad' href='/showlistsubtitles-2574-breaking-bad' >Breaking Bad</a>` +
				`</div></body></html>`))
		},
	})

	_, err := c.Search(context.Background(), Query{Title: "Breaking Bad", Language: "en"})
	if err == nil || !strings.Contains(err.Error(), "season") {
		t.Fatalf("want an error naming season/episode, got %v", err)
	}
}

// A movie and a series can share a name; without a season and episode the
// movie is what was meant.
func TestMovieWinsOverASeriesWhenNoEpisodeIsGiven(t *testing.T) {
	c, asked := testServer(t, nil)

	if _, err := c.Search(context.Background(), Query{Title: "Inception", Language: "en"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(*asked) < 2 || !strings.HasPrefix((*asked)[1], "/film-versions-127-") {
		t.Fatalf("want the movie page, asked: %v", *asked)
	}
}

// The gate page hands out the session cookie the file URL insists on, so the
// two requests have to share a jar — that is the whole reason Client keeps one.
func TestDownloadPassesTheGateAndKeepsTheSession(t *testing.T) {
	const body = "1\r\n00:00:01,000 --> 00:00:02,000\r\nhello\r\n"

	c, asked := testServer(t, map[string]http.HandlerFunc{
		"/downloads/EN-HIGH": func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "abc", Path: "/"})
			w.Write([]byte(gatePage("/download/film-1.srt")))
		},
		"/download/film-1.srt": func(w http.ResponseWriter, r *http.Request) {
			if _, err := r.Cookie("PHPSESSID"); err != nil {
				// What the live site does to a cookieless request.
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			if ref := r.Header.Get("Referer"); !strings.HasSuffix(ref, "/downloads/EN-HIGH") {
				t.Errorf("Referer = %q, want the gate page", ref)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="Inception.English-WWW.MY-SUBS.CO.srt"`)
			w.Write([]byte(body))
		},
	})

	data, name, err := c.Download(context.Background(), "/downloads/EN-HIGH")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != body {
		t.Errorf("data = %q", data)
	}
	if name != "Inception.English-WWW.MY-SUBS.CO.srt" {
		t.Errorf("name = %q", name)
	}
	if len(*asked) != 2 {
		t.Errorf("want gate + file, got %v", *asked)
	}
}

func TestDownloadReportsAnHTMLAnswer(t *testing.T) {
	c, _ := testServer(t, map[string]http.HandlerFunc{
		"/downloads/EN-HIGH": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(gatePage("/download/film-1.srt")))
		},
		"/download/film-1.srt": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.Write([]byte("<html>nope</html>"))
		},
	})

	if _, _, err := c.Download(context.Background(), "/downloads/EN-HIGH"); err == nil {
		t.Fatal("want an error when the site serves a page instead of the file")
	}
}

func TestDownloadUnwrapsAZippedSubtitle(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	if f, err := zw.Create("readme.txt"); err == nil {
		f.Write([]byte("ignore me"))
	}
	f, err := zw.Create("Inception.srt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("1\r\nsubtitle\r\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	c, _ := testServer(t, map[string]http.HandlerFunc{
		"/downloads/ZIP": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(gatePage("/download/film-2.zip")))
		},
		"/download/film-2.zip": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(archive.Bytes())
		},
	})

	data, name, err := c.Download(context.Background(), "/downloads/ZIP")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if name != "Inception.srt" || !strings.Contains(string(data), "subtitle") {
		t.Errorf("got %q / %q", name, data)
	}
}

func TestSearchWithNoResultsIsNotAnError(t *testing.T) {
	c, _ := testServer(t, map[string]http.HandlerFunc{
		"/search.php": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<html><body><div class="list-group">` +
				`<div class='list-group-item list-group-item-danger'>No result, Sorry</div></div></body></html>`))
		},
	})

	subs, err := c.Search(context.Background(), Query{Title: "nothing here", Language: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("want no results, got %d", len(subs))
	}
}

// A scraper's failure mode is a restyled page, and the app must survive one:
// no results is the right answer, a panic taking the window down is not.
func TestUnrecognisableMarkupYieldsNoResultsRatherThanFailing(t *testing.T) {
	pages := []struct{ name, body string }{
		{"empty", ""},
		{"truncated mid-tag", `<html><body><a class='list-group-item' href='/film-vers`},
		{"restyled away", `<html><body><main><ul><li>Inception</li></ul></main></body></html>`},
		{"cloudflare challenge", `<html><head><title>Just a moment...</title></head><body></body></html>`},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			c, _ := testServer(t, map[string]http.HandlerFunc{
				"/search.php": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(page.body)) },
			})

			subs, err := c.Search(context.Background(), Query{Title: "Inception", Language: "en"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(subs) != 0 {
				t.Fatalf("got %d results from unparseable markup", len(subs))
			}
		})
	}
}

// The same for the subtitle page: the title resolves, then the page it points
// at turns out to be unreadable.
func TestUnrecognisableSubtitlePageYieldsNoResults(t *testing.T) {
	c, _ := testServer(t, map[string]http.HandlerFunc{
		"/film-versions-127-inception-subtitles": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<html><body><h1>Movie Inception (2010) Subtitles</h1><p>nothing here</p></body></html>`))
		},
	})

	subs, err := c.Search(context.Background(), Query{Title: "Inception", Language: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("got %d results from a page with no entries", len(subs))
	}
}

// An outage is an error the caller can show, not something that hangs or
// panics — the rest of the app carries on without subtitles.
func TestSiteOutageIsAPlainError(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusForbidden} {
		c, _ := testServer(t, map[string]http.HandlerFunc{
			"/search.php": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) },
		})

		if _, err := c.Search(context.Background(), Query{Title: "Inception", Language: "en"}); err == nil {
			t.Errorf("status %d: want an error", status)
		}
	}
}

func TestSearchEscapesTheQuery(t *testing.T) {
	c, asked := testServer(t, nil)

	_, _ = c.Search(context.Background(), Query{Title: "Fast & Furious", Language: "en"})
	if len(*asked) == 0 || !strings.Contains((*asked)[0], "key=Fast+%26+Furious") {
		t.Fatalf("query not escaped: %v", *asked)
	}
}

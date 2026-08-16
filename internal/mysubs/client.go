// Package mysubs searches and downloads subtitles from my-subs.co.
//
// Unlike OpenSubtitles there is no API and no key: the site is plain server
// rendered HTML, and everything here is scraping. That is the whole point of
// this package — OpenSubtitles caps anonymous downloads at a handful per day,
// while my-subs.co has no per-user quota to hit.
//
// Three page shapes carry everything the client needs:
//
//	/search.php?key=…                            title lookup, two panels
//	/film-versions-{id}-{slug}-subtitles         a movie's subtitles
//	/versions-{id}-{episode}-{season}-{slug}-subtitles   one episode's subtitles
//
// Note the episode URL's number order: episode comes BEFORE season. That is the
// site's own ordering, confirmed against its season/episode dropdowns — the
// obvious reading is wrong and silently fetches a different episode.
package mysubs

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the English site. The fr./es. subdomains exist but carry
// the same subtitles — every language sits on the one page — so there is no
// reason to switch hosts per language.
const DefaultBaseURL = "https://my-subs.co"

// browserUA is sent because the site is behind Cloudflare, which answers a
// blank or scripted user agent with a challenge page instead of HTML.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// Client searches and downloads subtitles from my-subs.co.
//
// It keeps a cookie jar: the download flow hands out a PHP session on the gate
// page and the file URL answers 302 to anything without it.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a client. baseURL may be empty for DefaultBaseURL.
func New(baseURL string) *Client {
	jar, _ := cookiejar.New(nil) // only errors on a nil option list
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(defaultIfEmpty(baseURL, DefaultBaseURL)), "/"),
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}
}

// Query is one subtitle search. Season and Episode are set only for series; a
// movie leaves them at zero.
type Query struct {
	Title    string
	Year     string
	Language string // ISO 639-1 code, e.g. "fa". Empty means every language.
	Season   int
	Episode  int
}

// IsEpisode reports whether the query names one episode of a series.
func (q Query) IsEpisode() bool { return q.Season > 0 && q.Episode > 0 }

// Subtitle is one downloadable subtitle.
type Subtitle struct {
	// ID is the site-relative /downloads/… path. It is an opaque token that
	// Download turns into a file; it carries no meaning worth parsing.
	ID string

	Language      string // as the site labels it: "English", "Farsi/Persian", …
	Release       string // the release or version the subtitle was timed against
	Title         string // title as the subtitle page states it
	Year          string
	Uploader      string
	Age           string // "8 years ago", as shown; episode pages only
	DownloadCount int
}

// Search finds subtitles for one title, newest-and-most-downloaded first.
//
// It is two requests: the search page to resolve the title to its own page,
// then that page for the subtitle list. Matching is by title and year — the
// site exposes no IMDb id anywhere, so there is no precise key to search on.
func (c *Client) Search(ctx context.Context, q Query) ([]Subtitle, error) {
	title := strings.TrimSpace(q.Title)
	if title == "" {
		return nil, fmt.Errorf("need a title to search")
	}

	hits, err := c.findTitles(ctx, title)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}

	best, ok := pickTitle(hits, title, q.Year, q.IsEpisode())
	if !ok {
		return nil, nil
	}
	if best.series && !q.IsEpisode() {
		return nil, fmt.Errorf("%q is a TV series here — set a season and episode to search it", best.label)
	}

	page := best.path
	if best.series {
		page, err = episodePath(best.path, q.Season, q.Episode)
		if err != nil {
			return nil, err
		}
	}

	body, err := c.get(ctx, page, c.baseURL+"/")
	if err != nil {
		return nil, err
	}

	subs := parseSubtitlePage(string(body))
	subs = filterLanguage(subs, q.Language)
	sort.SliceStable(subs, func(i, j int) bool { return subs[i].DownloadCount > subs[j].DownloadCount })
	return subs, nil
}

// Download fetches one subtitle file.
//
// The /downloads/ link is a gate page, not the file: it holds a JavaScript
// countdown and the real path in a REAL_URL variable. The countdown is purely
// client side, but the session cookie the gate sets is not — the file URL
// answers 302 without it, which is why this is two requests through one jar.
func (c *Client) Download(ctx context.Context, id string) (data []byte, fileName string, err error) {
	gate := strings.TrimSpace(id)
	if gate == "" {
		return nil, "", fmt.Errorf("this result has no download link")
	}
	if !strings.HasPrefix(gate, "/") && !strings.HasPrefix(gate, "http") {
		gate = "/" + gate
	}

	page, err := c.get(ctx, gate, c.baseURL+"/")
	if err != nil {
		return nil, "", fmt.Errorf("open download page: %w", err)
	}

	real := realDownloadURL(string(page))
	if real == "" {
		return nil, "", fmt.Errorf("no download link on the page — the site layout may have changed")
	}

	body, header, err := c.getWithHeader(ctx, real, c.absolute(gate))
	if err != nil {
		return nil, "", fmt.Errorf("fetch subtitle: %w", err)
	}
	if isHTML(header.Get("Content-Type")) {
		return nil, "", fmt.Errorf("the site served a page instead of the subtitle file — try again")
	}

	name := dispositionName(header.Get("Content-Disposition"))
	if name == "" {
		name = pathBase(real)
	}

	// Some uploads are zipped. The player needs a subtitle file, not an
	// archive, so unwrap the first subtitle inside it.
	if inner, innerName, ok := unzipSubtitle(body); ok {
		return inner, innerName, nil
	}
	return body, name, nil
}

// titleHit is one row of the search page.
type titleHit struct {
	path   string
	label  string
	year   string
	series bool
}

func (c *Client) findTitles(ctx context.Context, title string) ([]titleHit, error) {
	body, err := c.get(ctx, "/search.php?key="+url.QueryEscape(title), c.baseURL+"/")
	if err != nil {
		return nil, err
	}
	return parseSearchPage(string(body)), nil
}

var (
	searchHitRe = regexp.MustCompile(`(?is)<a[^>]*href=['"](/(?:film-versions|showlistsubtitles)-[^'"]+)['"][^>]*>(.*?)</a>`)
	yearRe      = regexp.MustCompile(`\((\d{4})\)`)
)

func parseSearchPage(page string) []titleHit {
	var hits []titleHit
	for _, m := range searchHitRe.FindAllStringSubmatch(page, -1) {
		path, label := m[1], text(m[2])
		hit := titleHit{
			path:   path,
			label:  label,
			series: strings.HasPrefix(path, "/showlistsubtitles-"),
		}
		if y := yearRe.FindStringSubmatch(label); y != nil {
			hit.year = y[1]
		}
		hits = append(hits, hit)
	}
	return hits
}

// pickTitle scores the search hits against what was asked for. The site's own
// ordering is alphabetical rather than by relevance, so "Breaking Bad" lists
// "Breaking Bad Minisodes" first — taking the first hit is wrong.
func pickTitle(hits []titleHit, title, year string, wantSeries bool) (titleHit, bool) {
	want := normalize(title)

	best, bestScore := titleHit{}, -1<<30
	for _, hit := range hits {
		got := normalize(stripYear(hit.label))

		score := 0
		switch {
		case got == want:
			score += 100
		case strings.HasPrefix(got, want) || strings.HasPrefix(want, got):
			score += 60
		case strings.Contains(got, want) || strings.Contains(want, got):
			score += 30
		default:
			score -= 20
		}
		// A shorter title that still matches is the closer one: it has fewer
		// extra words bolted on ("Breaking Bad" over "Breaking Bad Minisodes").
		score -= len(got) / 8

		if hit.series == wantSeries {
			score += 50
		} else {
			score -= 50
		}
		if year != "" && hit.year != "" {
			if hit.year == year {
				score += 40
			} else {
				score -= 15
			}
		}

		if score > bestScore {
			best, bestScore = hit, score
		}
	}
	return best, bestScore > -1<<30
}

var showPathRe = regexp.MustCompile(`^/showlistsubtitles-(\d+)-(.+)$`)

// episodePath turns a show page path into one episode's page.
//
// The site orders the numbers episode-then-season, not the other way round.
func episodePath(showPath string, season, episode int) (string, error) {
	m := showPathRe.FindStringSubmatch(showPath)
	if m == nil {
		return "", fmt.Errorf("unexpected series link %q", showPath)
	}
	return fmt.Sprintf("/versions-%s-%d-%d-%s-subtitles", m[1], episode, season, m[2]), nil
}

var (
	// Movie pages: one anchor per subtitle, language in the flag's title,
	// release in <strong>, download count in <b>.
	filmEntryRe = regexp.MustCompile(`(?is)<a[^>]*href=["'](/downloads/[^"']+)["'][^>]*class=["']list-group-item["'][^>]*>(.*?)</a>`)
	flagTitleRe = regexp.MustCompile(`(?is)title=["']([^"']+)["']`)
	strongRe    = regexp.MustCompile(`(?is)<strong>(.*?)</strong>`)
	boldNumRe   = regexp.MustCompile(`(?is)<b>\s*(\d+)\s*</b>`)

	// Episode pages: subtitles are grouped under a version heading, each row
	// carrying its own language, uploader and count.
	versionSplitRe = regexp.MustCompile(`(?is)<div class=['"]version['"]>`)
	italicRe       = regexp.MustCompile(`(?is)<i>(.*?)</i>`)
	episodeRowRe   = regexp.MustCompile(`(?is)<div class=['"]lang['"]>.*?<i>(.*?)</i>.*?Uploaded by\s*:</b>\s*<a[^>]*>(.*?)</a>\s*([^<]*)<br>.*?Downloads\s*:</b>\s*(\d+).*?href=['"](/downloads/[^"']+)['"]`)
	// Fallback for a row that drops the uploader block.
	episodeRowLiteRe = regexp.MustCompile(`(?is)<div class=['"]lang['"]>.*?<i>(.*?)</i>.*?href=['"](/downloads/[^"']+)['"]`)

	h1Re      = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	spacesRe  = regexp.MustCompile(`\s+`)
	nonAlnum  = regexp.MustCompile(`[^a-z0-9]+`)
	realURLRe = regexp.MustCompile(`REAL_URL\s*=\s*"((?:[^"\\]|\\.)*)"`)
)

// parseSubtitlePage reads either page shape — a movie's or an episode's.
func parseSubtitlePage(page string) []Subtitle {
	title, year := pageTitle(page)

	subs := parseFilmEntries(page)
	if len(subs) == 0 {
		subs = parseEpisodeEntries(page)
	}
	for i := range subs {
		subs[i].Title = title
		subs[i].Year = year
	}
	return subs
}

func parseFilmEntries(page string) []Subtitle {
	var subs []Subtitle
	for _, m := range filmEntryRe.FindAllStringSubmatch(page, -1) {
		block := m[2]
		sub := Subtitle{ID: m[1], Release: text(firstGroup(strongRe, block))}
		if lang := flagTitleRe.FindStringSubmatch(block); lang != nil {
			sub.Language = text(lang[1])
		}
		if n := boldNumRe.FindStringSubmatch(block); n != nil {
			sub.DownloadCount, _ = strconv.Atoi(n[1])
		}
		subs = append(subs, sub)
	}
	return subs
}

func parseEpisodeEntries(page string) []Subtitle {
	var subs []Subtitle
	// The first chunk is everything before the first version heading, so it
	// holds no subtitle rows.
	for _, chunk := range versionSplitRe.Split(page, -1)[1:] {
		release := text(firstGroup(italicRe, chunk))

		rows := episodeRowRe.FindAllStringSubmatch(chunk, -1)
		if len(rows) == 0 {
			for _, m := range episodeRowLiteRe.FindAllStringSubmatch(chunk, -1) {
				subs = append(subs, Subtitle{ID: m[2], Language: text(m[1]), Release: release})
			}
			continue
		}
		for _, m := range rows {
			count, _ := strconv.Atoi(m[4])
			subs = append(subs, Subtitle{
				ID:            m[5],
				Language:      text(m[1]),
				Release:       release,
				Uploader:      text(m[2]),
				Age:           text(m[3]),
				DownloadCount: count,
			})
		}
	}
	return subs
}

// pageTitle pulls the display title and year out of the page heading, which
// reads "Movie Inception (2010) Subtitles" or
// "Breaking Bad Season 1 Episode 1 Subtitles".
func pageTitle(page string) (title, year string) {
	heading := text(firstGroup(h1Re, page))
	heading = strings.TrimSuffix(strings.TrimSpace(heading), "Subtitles")
	heading = strings.TrimPrefix(strings.TrimSpace(heading), "Movie ")
	if y := yearRe.FindStringSubmatch(heading); y != nil {
		year = y[1]
	}
	return strings.TrimSpace(stripYear(heading)), year
}

// languageAliases maps the ISO codes the app's picker uses onto the names the
// site prints. Matching is substring and case insensitive on purpose: the same
// language appears as "english", "Persian", "Farsi/Persian" and
// "Portuguese (Brazilian)" on one page.
var languageAliases = map[string][]string{
	"en": {"english"},
	"fa": {"persian", "farsi"},
	"ar": {"arabic"},
	"fr": {"french"},
	"de": {"german"},
	"es": {"spanish"},
	"it": {"italian"},
	"tr": {"turkish"},
	"ru": {"russian"},
	"pt": {"portuguese"},
	"nl": {"dutch"},
	"sv": {"swedish"},
	"he": {"hebrew"},
	"hi": {"hindi"},
	"ur": {"urdu"},
	"id": {"indonesian"},
	"ja": {"japanese"},
	"ko": {"korean"},
	"zh": {"chinese"},
	"pl": {"polish"},
	"ro": {"romanian"},
	"el": {"greek"},
	"cs": {"czech"},
	"da": {"danish"},
	"fi": {"finnish"},
	"no": {"norwegian"},
	"hu": {"hungarian"},
	"th": {"thai"},
	"vi": {"vietnamese"},
	"uk": {"ukrainian"},
	"bg": {"bulgarian"},
	"hr": {"croatian"},
	"sr": {"serbian"},
	"sl": {"slovenian"},
	"sk": {"slovak"},
	"bn": {"bengali"},
	"ms": {"malay"},
	"et": {"estonian"},
	"lt": {"lithuanian"},
	"mk": {"macedonian"},
	"sq": {"albanian"},
	"bs": {"bosnian"},
	"is": {"icelandic"},
}

func filterLanguage(subs []Subtitle, code string) []Subtitle {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return subs
	}
	names, ok := languageAliases[code]
	if !ok {
		names = []string{code}
	}

	kept := make([]Subtitle, 0, len(subs))
	for _, sub := range subs {
		got := strings.ToLower(sub.Language)
		for _, name := range names {
			if strings.Contains(got, name) {
				kept = append(kept, sub)
				break
			}
		}
	}
	return kept
}

// realDownloadURL reads the file path out of the gate page's countdown script,
// where it is a JavaScript string literal: "\/download\/film-791211.srt".
func realDownloadURL(page string) string {
	m := realURLRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return unescapeJS(m[1])
}

func unescapeJS(s string) string {
	return strings.NewReplacer(`\/`, "/", `\"`, `"`, `\\`, `\`, `\'`, "'").Replace(s)
}

// unzipSubtitle unwraps a zipped upload, returning the first subtitle in it.
func unzipSubtitle(data []byte) (inner []byte, name string, ok bool) {
	if len(data) < 4 || !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return nil, "", false
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", false
	}
	for _, f := range r.File {
		switch strings.ToLower(pathExt(f.Name)) {
		case ".srt", ".ass", ".ssa", ".sub", ".vtt":
		default:
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		rc.Close()
		if err != nil || len(content) == 0 {
			continue
		}
		return content, pathBase(f.Name), true
	}
	return nil, "", false
}

func (c *Client) get(ctx context.Context, ref, referer string) ([]byte, error) {
	body, _, err := c.getWithHeader(ctx, ref, referer)
	return body, err
}

func (c *Client) getWithHeader(ctx context.Context, ref, referer string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.absolute(ref), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	// Pages run a few hundred KB; the cap is only there so a misdirected URL
	// cannot exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("my-subs.co answered %s", resp.Status)
	}
	return body, resp.Header, nil
}

// absolute resolves a site-relative path against the configured base. Links on
// the pages are relative, and Download is also handed IDs straight back from
// Search, so both shapes arrive here.
func (c *Client) absolute(ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if !strings.HasPrefix(ref, "/") {
		ref = "/" + ref
	}
	return c.baseURL + ref
}

func dispositionName(header string) string {
	if header == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	return pathBase(params["filename"])
}

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

// text turns a fragment of markup into the plain string it renders as.
func text(fragment string) string {
	s := tagRe.ReplaceAllString(fragment, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func normalize(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), " "), " ")
}

func stripYear(s string) string {
	return strings.TrimSpace(yearRe.ReplaceAllString(s, ""))
}

// pathBase and pathExt work on URL and zip paths, which use forward slashes
// whatever the host OS does — filepath would mangle them on Windows.
func pathBase(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSpace(p)
}

func pathExt(p string) string {
	base := pathBase(p)
	if i := strings.LastIndex(base, "."); i >= 0 {
		return base[i:]
	}
	return ""
}

func defaultIfEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

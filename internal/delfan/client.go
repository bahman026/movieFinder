// Package delfan talks to the "Delfan" movie app's signed HTTP API.
//
// The request signing was reverse-engineered from the app. Two fields on every
// gated request are computed client-side:
//
//   - body: a fixed concatenation built once per session from the login token
//     (see buildBody). The two MD5 halves are padding the server cannot verify;
//     the real payload is the session token embedded in the middle.
//   - an: a rolling anti-replay nonce, MD5(q1 + q2 + 101), where q1 and q2 are
//     integers the server returns in every response. Each gated request must
//     carry the nonce derived from the PREVIOUS response's q1/q2, so the client
//     threads that state through every call (see roundtrip).
//
// The nonce is per-host: login happens on one host, but the gated endpoints
// live on another, so a vitrin call on the gated host seeds its q1/q2 before
// the first real request (see ensureSession).
package delfan

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"moviefinder/internal/safe"
)

// Signing constants, recovered from the decompiled app. These are part of the
// signing algorithm rather than of the deployment, so changing one without
// changing the code around it would not produce a valid request.
const (
	userAgent  = "Dalvik/2.1.0 (Linux; U; Android 9; SM-N975F Build/PI)"
	constMid1  = "fdaa94a151e2c5d4"
	constMid2  = "74a290e8"
	constTail  = "y87mdjsodon"
	nonceConst = 101 // 84 + 9 + 8, from g.t() with the recovered helper constants
)

// Everything this server rotates. The hosts move (the app rediscovers them from
// fragmented fields in each response), and the path, endpoint names, key and
// app version have all changed across app releases — so each is a default here
// and an overridable setting, rather than something that needs a new build.
const (
	DefaultLoginHost     = "http://googfilmazappfordownfilmmedis.xyz"
	DefaultAPIHost       = "http://tahlilgaraan.ir"
	DefaultBasePath      = "/app-plus"
	DefaultLoginEndpoint = "users.php"
	DefaultAPIEndpoint   = "vp1.php"
	DefaultAPIKey        = "pwep5d4sdoe0ewsosa7d563d"
	DefaultAppVersion    = "c215sfxd545fgs" // R.string.appversion
)

// loginURL is the ungated login endpoint, on the login host.
func (c *Client) loginURL() string {
	return c.opts.LoginHost + c.opts.BasePath + "/" + c.opts.LoginEndpoint +
		"?key=" + c.opts.APIKey + "&action=login"
}

// actionURL is a gated endpoint, on the API host. Both hosts share the base
// path, matching how the server lays these out.
func (c *Client) actionURL(action string) string {
	return c.opts.APIHost + c.opts.BasePath + "/" + c.opts.APIEndpoint +
		"?key=" + c.opts.APIKey + "&action=" + action
}

// Options is the full endpoint shape. Every empty field falls back to its
// Default above, so a caller can override only what has moved.
type Options struct {
	LoginHost     string // host serving the login endpoint
	APIHost       string // host serving the gated endpoints
	BasePath      string // path prefix shared by both, e.g. /app-plus
	LoginEndpoint string // file name of the login endpoint, e.g. users.php
	APIEndpoint   string // file name of the gated endpoint, e.g. vp1.php
	APIKey        string // the key= query parameter on every request
	AppVersion    string // appversion, which is folded into the signed body
}

// withDefaults fills every empty field from the constants above.
func (o Options) withDefaults() Options {
	fill := func(v, def string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return strings.TrimSpace(v)
	}
	o.LoginHost = strings.TrimRight(fill(o.LoginHost, DefaultLoginHost), "/")
	o.APIHost = strings.TrimRight(fill(o.APIHost, DefaultAPIHost), "/")
	// A base path is stored with a leading slash and no trailing one, so the
	// URL builder can always join with a single "/".
	base := strings.Trim(fill(o.BasePath, DefaultBasePath), "/")
	if base == "" {
		o.BasePath = ""
	} else {
		o.BasePath = "/" + base
	}
	o.LoginEndpoint = strings.TrimLeft(fill(o.LoginEndpoint, DefaultLoginEndpoint), "/")
	o.APIEndpoint = strings.TrimLeft(fill(o.APIEndpoint, DefaultAPIEndpoint), "/")
	o.APIKey = fill(o.APIKey, DefaultAPIKey)
	o.AppVersion = fill(o.AppVersion, DefaultAppVersion)
	return o
}

// Client is a stateful, thread-safe Delfan API client. It logs in lazily and
// threads the rolling nonce through every call.
type Client struct {
	opts Options
	http *http.Client

	// mu serializes gated requests: the rolling nonce means two concurrent
	// requests would reuse the same q1/q2 and one would be rejected, so a
	// request holds mu across compute-send-update.
	mu       sync.Mutex
	loggedIn bool
	auth     string // session token, embedded in body
	us       string // u_s, the login night_mode
	body     string // built once per session
	q1, q2   int    // latest server nonce inputs
}

// New builds a client with only the hosts overridden. Empty hosts fall back to
// the current defaults.
func New(loginHost, apiHost string) *Client {
	return NewWithOptions(Options{LoginHost: loginHost, APIHost: apiHost})
}

// NewWithOptions builds a client with the full endpoint shape overridable.
// Empty fields fall back to their defaults.
func NewWithOptions(opts Options) *Client {
	return &Client{
		opts: opts.withDefaults(),
		http: &http.Client{
			Timeout: 30 * time.Second,
			// Do not auto-follow redirects: the download resolver hands back a
			// 302 whose Location is the real file, which we want to read rather
			// than fetch.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Home returns the app's landing content, flattened into one list: newly added
// movies and series, plus featured titles, de-duplicated by id.
func (c *Client) Home(ctx context.Context) ([]Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}

	var resp vitrinResponse
	if err := c.roundtrip(ctx, "vitrin", url.Values{}, &resp); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var items []Item
	for _, group := range [][]Item{resp.NewMovie, resp.NewSerie, resp.UpdatedSerie, resp.Vige} {
		for _, it := range group {
			if it.ID == "" || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			items = append(items, it)
		}
	}
	return items, nil
}

// Search returns titles matching query. It is paged; page starts at 1.
func (c *Client) Search(ctx context.Context, query string, page int) ([]Item, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search text is empty")
	}
	if page < 1 {
		page = 1
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}

	form := url.Values{
		"country": {""}, "year_to": {""}, "sort_by": {""}, "type": {""},
		"dub": {""}, "year_from": {""}, "imdb": {""}, "genre": {""},
		"StateSerie": {""}, "search_text": {query},
	}
	var resp searchResponse
	if err := c.roundtrip(ctx, "filter_search&pageno="+strconv.Itoa(page), form, &resp); err != nil {
		return nil, err
	}
	if strings.EqualFold(resp.StateAll, "F") {
		return nil, fmt.Errorf("%s", firstNonEmpty(resp.Msg, "the server rejected the request"))
	}
	return resp.All, nil
}

// SearchCast finds people (actors/directors) matching a name. The first result
// is usually the best match.
func (c *Client) SearchCast(ctx context.Context, query string) ([]Cast, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("cast name is empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}

	var resp searchCastResponse
	if err := c.roundtrip(ctx, "search_cast&pageno=1", url.Values{"q": {query}, "cast_type": {"cast"}}, &resp); err != nil {
		return nil, err
	}
	if strings.EqualFold(resp.StateAll, "F") {
		return nil, fmt.Errorf("%s", firstNonEmpty(resp.Msg, "the server rejected the request"))
	}

	casts := make([]Cast, 0, len(resp.MovieList))
	for _, p := range resp.MovieList {
		casts = append(casts, Cast{
			ID:   strconv.Itoa(asInt(p.ID)),
			Name: p.Name,
			Role: p.ActionUser,
			Pic:  p.PicURL,
		})
	}
	return casts, nil
}

// CastMovies returns one page of a person's movies, plus their bio.
func (c *Client) CastMovies(ctx context.Context, castID string, page int) ([]Item, string, error) {
	if page < 1 {
		page = 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, "", err
	}

	var resp castMoviesResponse
	if err := c.roundtrip(ctx, "show_movie_cast&pageno="+strconv.Itoa(page),
		url.Values{"cast_id": {castID}, "cast_type": {"cast"}}, &resp); err != nil {
		return nil, "", err
	}
	if strings.EqualFold(resp.StateAll, "F") {
		return nil, "", fmt.Errorf("%s", firstNonEmpty(resp.Msg, "the server rejected the request"))
	}
	return resp.MovieList, resp.Bio, nil
}

// Details fetches one title, including its download links.
func (c *Client) Details(ctx context.Context, id string) (Detail, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return Detail{}, err
	}

	var resp detialsResponse
	if err := c.roundtrip(ctx, "detials", url.Values{"is_mobile": {"1"}, "id": {id}}, &resp); err != nil {
		return Detail{}, err
	}
	if strings.EqualFold(resp.StateAll, "F") {
		return Detail{}, fmt.Errorf("%s", firstNonEmpty(resp.Msg, "the server rejected the request"))
	}
	if len(resp.Detiles) == 0 {
		return Detail{}, fmt.Errorf("no details returned for id %s", id)
	}

	d := resp.Detiles[0]
	detail := Detail{
		ID:            id,
		Title:         d.Title,
		OriginalTitle: originalTitle(d.Description),
		IMDBRating:    d.IMDBRating,
		Description:   cleanDescription(d.Description),
		Year:          d.Year,
		PosterURL:     d.PosterURL,
		ThumbnailURL:  d.ThumbnailURL,
		TrailerURL:    d.TrailerURL,
		IsMovie:       d.IsMovie == "1",
	}

	// download_link is a flat list for films and a season/episode tree for
	// series; decode whichever this is.
	if detail.IsMovie {
		_ = json.Unmarshal(d.DownloadLink, &detail.DownloadLinks)
	} else {
		var seasons []seriesSeason
		if err := json.Unmarshal(d.DownloadLink, &seasons); err == nil {
			for _, s := range seasons {
				if len(s.Links) == 0 {
					continue
				}
				detail.Seasons = append(detail.Seasons, Season{Name: s.SessionTitle, Episodes: s.Links})
			}
		}
	}
	return detail, nil
}

// ResolveLinks resolves every link's play.php redirect to its real file URL,
// concurrently. The returned slice is parallel to links; an entry is empty when
// that link could not be resolved. Resolution does not touch the nonce state,
// so it needs no lock.
func (c *Client) ResolveLinks(ctx context.Context, links []DownloadLink) []string {
	out := make([]string, len(links))
	var wg sync.WaitGroup
	for i, l := range links {
		wg.Add(1)
		go func(i int, l DownloadLink) {
			defer wg.Done()
			// Guarded: one malformed redirect must not panic the app. An entry
			// that fails is left empty, which callers already handle.
			safe.Run(func() {
				if url, err := c.ResolveDownloadURL(ctx, l); err == nil {
					out[i] = url
				}
			}, nil)
		}(i, l)
	}
	wg.Wait()
	return out
}

// ResolveDownloadURL follows a link's play.php redirect to the real, signed
// file URL. That URL expires, so it is fetched on demand rather than stored.
func (c *Client) ResolveDownloadURL(ctx context.Context, link DownloadLink) (string, error) {
	if strings.TrimSpace(link.Link) == "" {
		return "", fmt.Errorf("this link has no URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.Link, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve download: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if loc := resp.Header.Get("Location"); loc != "" {
		return loc, nil
	}
	// Some links may serve the file directly rather than redirect.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return link.Link, nil
	}
	return "", fmt.Errorf("resolve download: server answered %s", resp.Status)
}

// ensureSession logs in and seeds the gated host's nonce. Caller holds c.mu.
func (c *Client) ensureSession(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}

	// 1. Log in (anonymous is fine) for the session token and u_s.
	form := url.Values{
		"user_name": {""}, "apname": {"Delfan"}, "is_tv": {"No"},
		"app_verion": {"1"}, "android_id": {"95bf8f966958ca70"}, "version_sp": {"v1"}, "token": {""},
	}
	var login loginResponse
	if err := c.request(ctx, c.loginURL(), form, &login); err != nil {
		return fmt.Errorf("delfan login: %w", err)
	}
	if len(login.Infos) == 0 || login.Infos[0].Auth == "" {
		return fmt.Errorf("delfan login returned no session token")
	}
	c.auth = login.Infos[0].Auth
	c.us = login.NightMode
	c.body = buildBody(c.auth, c.opts.AppVersion)
	c.q1, c.q2 = asInt(login.Q1), asInt(login.Q2)
	c.loggedIn = true

	// 2. vitrin on the gated host seeds ITS q1/q2. vitrin does not validate the
	// nonce, so the seed value it is called with does not matter.
	var v vitrinResponse
	if err := c.roundtrip(ctx, "vitrin", url.Values{}, &v); err != nil {
		c.loggedIn = false
		return fmt.Errorf("delfan session seed: %w", err)
	}
	return nil
}

// roundtrip performs one gated request: it signs with the current nonce, sends,
// then advances the nonce from the response. Caller holds c.mu. out must be a
// pointer to a struct carrying q1/q2 so the chain can advance.
func (c *Client) roundtrip(ctx context.Context, action string, form url.Values, out nonceCarrier) error {
	form.Set("s_n", "1")
	form.Set("user_name", "")
	form.Set("u_s", c.us)
	form.Set("apname", "Delfan")
	form.Set("body", c.body)
	form.Set("langueg", "")
	form.Set("an", nonce(c.q1, c.q2))
	form.Set("token", "")

	if err := c.request(ctx, c.actionURL(action), form, out); err != nil {
		return err
	}
	// Advance the rolling nonce from this response.
	if q1, q2, ok := out.nonce(); ok {
		c.q1, c.q2 = q1, q2
	}
	return nil
}

func (c *Client) request(ctx context.Context, target string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server answered %s", resp.Status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// buildBody assembles the per-session body field. See the package doc.
// appVersion is passed in rather than read from a constant so the value stays
// configurable without making this a method.
func buildBody(auth, appVersion string) string {
	head := md5hex(strconv.Itoa(rand.Intn(500)) + "cotation")
	tail := md5hex(time.Now().Format("Mon Jan 02 15:04:05 MST 2006") + "cotation")
	return head + auth + constMid1 + constMid2 + auth + constTail + appVersion + tail
}

// nonce is the rolling anti-replay value: MD5(q1 + q2 + 101).
func nonce(q1, q2 int) string {
	return md5hex(strconv.Itoa(q1 + q2 + nonceConst))
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// nonceCarrier lets roundtrip advance the chain from any response type.
type nonceCarrier interface {
	nonce() (q1, q2 int, ok bool)
}

func (r *vitrinResponse) nonce() (int, int, bool)     { return asInt(r.Q1), asInt(r.Q2), r.Q1 != nil }
func (r *searchResponse) nonce() (int, int, bool)     { return asInt(r.Q1), asInt(r.Q2), r.Q1 != nil }
func (r *detialsResponse) nonce() (int, int, bool)    { return asInt(r.Q1), asInt(r.Q2), r.Q1 != nil }
func (r *searchCastResponse) nonce() (int, int, bool) { return asInt(r.Q1), asInt(r.Q2), r.Q1 != nil }
func (r *castMoviesResponse) nonce() (int, int, bool) { return asInt(r.Q1), asInt(r.Q2), r.Q1 != nil }

// asInt reads a q1/q2 value that the API sends as either a JSON number or a
// JSON string.
func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// originalTitle pulls the English title out of a description that starts with
// "نام اصلی : The Eyes<br>...", for a better subtitle search.
func originalTitle(description string) string {
	const marker = "نام اصلی :"
	i := strings.Index(description, marker)
	if i < 0 {
		return ""
	}
	rest := description[i+len(marker):]
	if j := strings.Index(rest, "<br>"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// cleanDescription turns the API's <br> and \r markup into plain newlines.
func cleanDescription(description string) string {
	r := strings.NewReplacer("<br>", "\n", "\r", "", "<br/>", "\n", "<br />", "\n")
	return strings.TrimSpace(r.Replace(description))
}

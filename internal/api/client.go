// Package api talks to the movie site's HTTP API.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"moviefinder/internal/config"
)

// Client calls the API, failing over between the configured hosts.
type Client struct {
	cfg  config.Config
	http *http.Client

	// mu guards active, the index of the host that answered last. Requests
	// start there rather than at the top of the list, so once a mirror is
	// known to be down it is not retried on every single call.
	mu     sync.RWMutex
	active int
}

// New builds a client for the given settings.
func New(cfg config.Config) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout:   time.Duration(cfg.TimeoutSeconds) * time.Second,
			Transport: transport,
		},
	}
}

// Config returns the settings the client was built with.
func (c *Client) Config() config.Config { return c.cfg }

// ActiveHost is the host that answered most recently, for display in the UI.
func (c *Client) ActiveHost() string {
	hosts := c.cfg.CleanHosts()
	if len(hosts) == 0 {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.active < 0 || c.active >= len(hosts) {
		return hosts[0]
	}
	return hosts[c.active]
}

// Movies returns one page of the main listing. Page numbering starts at 1 and
// the page size is whatever the server decides.
func (c *Client) Movies(ctx context.Context, page int) ([]Movie, error) {
	if page < 1 {
		page = 1
	}
	body, err := c.get(ctx, "get_movies", url.Values{"page": {strconv.Itoa(page)}})
	if err != nil {
		return nil, err
	}
	return decodeMovies(body)
}

// Search returns everything matching query, with films first, then series,
// then TV channels. The endpoint is not paged.
func (c *Client) Search(ctx context.Context, query string) ([]Movie, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search text is empty")
	}
	body, err := c.get(ctx, "search", url.Values{"q": {query}})
	if err != nil {
		return nil, err
	}
	if err := checkAPIError(body); err != nil {
		return nil, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	results := make([]Movie, 0, len(resp.Movie)+len(resp.TVSeries)+len(resp.TVChannels))
	results = append(results, resp.Movie...)
	results = append(results, resp.TVSeries...)
	results = append(results, resp.TVChannels...)
	return results, nil
}

// MoviesByGenre returns one page of a genre listing.
func (c *Client) MoviesByGenre(ctx context.Context, genreID string, page int) ([]Movie, error) {
	if page < 1 {
		page = 1
	}
	body, err := c.get(ctx, "get_movie_by_genre_id", url.Values{
		"id":   {genreID},
		"page": {strconv.Itoa(page)},
	})
	if err != nil {
		return nil, err
	}
	return decodeMovies(body)
}

// Slider returns the featured titles shown on the site's home page.
func (c *Client) Slider(ctx context.Context) ([]Movie, error) {
	body, err := c.get(ctx, "get_slider", nil)
	if err != nil {
		return nil, err
	}
	if err := checkAPIError(body); err != nil {
		return nil, err
	}
	var resp struct {
		SliderType string  `json:"slider_type"`
		Data       []Movie `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode slider: %w", err)
	}
	return resp.Data, nil
}

// Details fetches one title. kind is "movie" or "tvseries" — use Movie.Kind().
func (c *Client) Details(ctx context.Context, kind, id string) (Detail, error) {
	body, err := c.get(ctx, "get_single_details", url.Values{
		"type": {kind},
		"id":   {id},
	})
	if err != nil {
		return Detail{}, err
	}
	if err := checkAPIError(body); err != nil {
		return Detail{}, err
	}
	var detail Detail
	if err := json.Unmarshal(body, &detail); err != nil {
		return Detail{}, fmt.Errorf("decode details: %w", err)
	}
	return detail, nil
}

// Image fetches a poster or thumbnail.
//
// Detail responses hand back image URLs on a CDN host that no longer resolves,
// while the same path is served by the API host itself, so a failed fetch is
// retried against the host that is currently answering.
func (c *Client) Image(ctx context.Context, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("no image URL")
	}

	body, err := c.fetchImage(ctx, rawURL)
	if err == nil {
		return body, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	parsed, parseErr := url.Parse(rawURL)
	if parseErr != nil || parsed.Path == "" {
		return nil, err
	}
	fallback := c.ActiveHost() + parsed.Path
	if fallback == rawURL {
		return nil, err
	}
	if body, altErr := c.fetchImage(ctx, fallback); altErr == nil {
		return body, nil
	}
	return nil, err
}

func (c *Client) fetchImage(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("image %s: %s", target, resp.Status)
	}
	// Posters are small; the cap keeps a misdirected URL from filling memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("image %s: empty response", target)
	}
	return body, nil
}

const userAgent = "MovieFinder/0.1"

// get calls endpoint on the first host that answers, starting from the one
// that worked last. A host is only abandoned for transport-level failures and
// 5xx responses; an API-level error is a real answer and is returned as-is
// rather than sending the same doomed request to every mirror.
func (c *Client) get(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	if err := c.cfg.Validate(); err != nil {
		return nil, err
	}
	hosts := c.cfg.CleanHosts()

	query := c.commonParams()
	for key, values := range params {
		for _, v := range values {
			query.Set(key, v)
		}
	}

	c.mu.RLock()
	start := c.active
	c.mu.RUnlock()
	if start < 0 || start >= len(hosts) {
		start = 0
	}

	var failures []string
	for offset := range hosts {
		index := (start + offset) % len(hosts)
		host := hosts[index]

		target := host + c.cfg.BasePath + "/" + endpoint + "?" + query.Encode()
		body, tryNext, err := c.fetch(ctx, target)
		if err == nil {
			c.mu.Lock()
			c.active = index
			c.mu.Unlock()
			return body, nil
		}
		// A cancelled request means the caller moved on, not a dead host.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A request the server actively rejected would be rejected the same
		// way everywhere, so stop rather than burn through the mirrors.
		if !tryNext {
			return nil, fmt.Errorf("%s: %w", host, err)
		}
		failures = append(failures, host+": "+err.Error())
	}

	return nil, fmt.Errorf("all %d host(s) failed:\n  %s", len(hosts), strings.Join(failures, "\n  "))
}

func (c *Client) commonParams() url.Values {
	query := url.Values{}
	query.Set("api_secret_key", c.cfg.APISecretKey)
	query.Set("version", c.cfg.Version)
	query.Set("country", c.cfg.Country)
	if c.cfg.SP {
		query.Set("sp", "true")
	}
	return query
}

// fetch performs one request. The bool reports whether the failure is the
// host's fault and therefore worth retrying against another mirror.
func (c *Client) fetch(ctx context.Context, target string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		// Refused, timed out, DNS failure, TLS failure — exactly what the
		// mirror list exists for.
		return nil, true, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("server answered %s", resp.Status)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, false, fmt.Errorf("answered %s", resp.Status)
	}

	// Cap the read so a misdirected URL cannot exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, true, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, true, fmt.Errorf("empty response")
	}
	return body, false, nil
}

// decodeMovies reads the bare-array shape used by the listing endpoints.
func decodeMovies(body []byte) ([]Movie, error) {
	if err := checkAPIError(body); err != nil {
		return nil, err
	}
	var movies []Movie
	if err := json.Unmarshal(body, &movies); err != nil {
		return nil, fmt.Errorf("decode listing: %w", err)
	}
	return movies, nil
}

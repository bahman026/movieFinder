// Package opensubtitles searches and downloads subtitles from
// api.opensubtitles.com.
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// currentBaseURL is a var rather than a const so tests can point it at a
// local httptest server instead of the real API.
var currentBaseURL = "https://api.opensubtitles.com/api/v1"

// RegisterURL is where a free API key is obtained. Both search and download
// require one — without it the API answers "You cannot consume this service"
// rather than any subtitle data, confirmed against the live service.
const RegisterURL = "https://www.opensubtitles.com/en/consumers"

// DefaultAPIKey is the built-in key, so subtitle search works out of the box.
// A key the user enters in Settings overrides it (see ResolveKey). It is never
// shown in the Settings field — an empty field there means "use this default".
//
// It is EMPTY here on purpose. The real key lives in key.go, which is
// gitignored: it is compiled into the binary but never committed, so the secret
// stays out of the repository while needing nothing external at build or run
// time. If key.go is absent the build still works with no built-in key. Keep
// this an assignable var — key.go's init() overwrites it.
var DefaultAPIKey = ""

// ResolveKey returns the user's key when they have set one, otherwise the
// built-in DefaultAPIKey.
func ResolveKey(userKey string) string {
	if k := strings.TrimSpace(userKey); k != "" {
		return k
	}
	return DefaultAPIKey
}

// Client searches and downloads subtitles.
//
// Downloads made without logging in are quota-limited per IP by OpenSubtitles,
// typically a handful per day; this client does not implement login, since the
// anonymous quota is enough for occasional use.
type Client struct {
	apiKey string
	http   *http.Client
}

// New builds a client. apiKey may be empty; every call then fails fast with a
// message pointing at RegisterURL instead of making a doomed request.
func New(apiKey string) *Client {
	return &Client{
		apiKey: strings.TrimSpace(apiKey),
		http:   &http.Client{Timeout: 20 * time.Second},
	}
}

// Subtitle is one search result, flattened from the API's nested attributes.
type Subtitle struct {
	ID            string
	Language      string
	Release       string
	MovieName     string
	Year          string
	UploadDate    time.Time
	DownloadCount int
	Rating        float64
	HD            bool
	// FileID is what Download needs. A release can carry more than one file
	// (multi-CD splits); this is the first one.
	FileID   int
	FileName string
}

// UploadDateLabel renders UploadDate, blank when the API did not supply one.
func (s Subtitle) UploadDateLabel() string {
	if s.UploadDate.IsZero() {
		return ""
	}
	return s.UploadDate.Format("2006-01-02")
}

// Search finds subtitles for a title.
//
// imdbID, when non-empty, searches by that identifier, which OpenSubtitles
// matches far more precisely than free text — pass the numeric id without the
// "tt" prefix. If that comes back empty, Search retries once with title and
// year instead, since the two lookups are indexed independently and one being
// empty does not mean the other will be.
func (c *Client) Search(ctx context.Context, title, imdbID, year, language string) ([]Subtitle, error) {
	return c.SearchEpisode(ctx, title, imdbID, year, language, 0, 0)
}

// SearchEpisode is Search narrowed to one episode of a series; season and
// episode are both zero for a film, which makes it plain Search.
//
// The identifier changes meaning for an episode: OpenSubtitles indexes every
// episode under its own imdb_id, while the id this app carries is the series'
// one — so it goes in parent_imdb_id, next to the season and episode numbers.
// The year is dropped too, since it would be read as the episode's air year.
func (c *Client) SearchEpisode(ctx context.Context, title, imdbID, year, language string, season, episode int) ([]Subtitle, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("add your OpenSubtitles API key in Settings — get a free one at %s", RegisterURL)
	}
	if language == "" {
		language = "en"
	}
	episodic := season > 0 && episode > 0

	if imdbID != "" {
		q := url.Values{"languages": {language}}
		if episodic {
			q.Set("parent_imdb_id", imdbID)
		} else {
			q.Set("imdb_id", imdbID)
		}
		addEpisode(q, season, episode)

		subs, err := c.search(ctx, q)
		if err != nil {
			return nil, err
		}
		if len(subs) > 0 {
			return subs, nil
		}
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("need a title or an IMDb id to search")
	}
	q := url.Values{"query": {title}, "languages": {language}}
	if year != "" && !episodic {
		q.Set("year", year)
	}
	addEpisode(q, season, episode)
	return c.search(ctx, q)
}

func addEpisode(q url.Values, season, episode int) {
	if season > 0 && episode > 0 {
		q.Set("season_number", strconv.Itoa(season))
		q.Set("episode_number", strconv.Itoa(episode))
	}
}

func (c *Client) search(ctx context.Context, query url.Values) ([]Subtitle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentBaseURL+"/subtitles?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(req)

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode subtitles: %w", err)
	}

	subs := make([]Subtitle, 0, len(resp.Data))
	for _, item := range resp.Data {
		a := item.Attributes
		if len(a.Files) == 0 {
			continue // nothing to download for this entry
		}
		file := a.Files[0]
		subs = append(subs, Subtitle{
			ID:            item.ID,
			Language:      a.Language,
			Release:       a.Release,
			MovieName:     firstNonEmpty(a.FeatureDetails.MovieName, a.FeatureDetails.Title),
			Year:          a.FeatureDetails.Year.String(),
			UploadDate:    parseTime(a.UploadDate),
			DownloadCount: a.DownloadCount,
			Rating:        a.Ratings,
			HD:            a.HD,
			FileID:        file.FileID,
			FileName:      file.FileName,
		})
	}
	return subs, nil
}

// Download fetches one subtitle file. OpenSubtitles hands back a signed,
// short-lived link rather than the file itself, so this is a two-step fetch.
func (c *Client) Download(ctx context.Context, fileID int) (data []byte, fileName string, err error) {
	if c.apiKey == "" {
		return nil, "", fmt.Errorf("add your OpenSubtitles API key in Settings — get a free one at %s", RegisterURL)
	}
	if fileID == 0 {
		return nil, "", fmt.Errorf("this result has no file to download")
	}

	payload, _ := json.Marshal(map[string]int{"file_id": fileID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, currentBaseURL+"/download", bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("build download request: %w", err)
	}
	c.setHeaders(req)

	body, err := c.do(req)
	if err != nil {
		return nil, "", err
	}

	var resp downloadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("decode download response: %w", err)
	}
	if resp.Link == "" {
		if resp.Message != "" {
			return nil, "", fmt.Errorf("%s", resp.Message)
		}
		return nil, "", fmt.Errorf("no download link in the response")
	}

	fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resp.Link, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build file request: %w", err)
	}
	fileResp, err := c.http.Do(fileReq)
	if err != nil {
		return nil, "", fmt.Errorf("fetch subtitle file: %w", err)
	}
	defer fileResp.Body.Close()

	if fileResp.StatusCode < 200 || fileResp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("subtitle file: server answered %s", fileResp.Status)
	}

	// Subtitle files are small; the cap is only there against a misdirected URL.
	data, err = io.ReadAll(io.LimitReader(fileResp.Body, 8<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read subtitle file: %w", err)
	}

	name := resp.FileName
	if name == "" {
		name = "subtitle.srt"
	}
	return data, name, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "MovieFinder v0.1")
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", extractMessage(body, resp.Status))
	}
	return body, nil
}

// extractMessage prefers the API's own {"message": "..."} envelope over a bare
// HTTP status, since it names the actual problem (missing key, quota, ...).
func extractMessage(body []byte, fallback string) string {
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Message != "" {
		return e.Message
	}
	return fallback
}

// searchResponse is the /subtitles payload shape.
type searchResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Language      string  `json:"language"`
			Release       string  `json:"release"`
			DownloadCount int     `json:"download_count"`
			Ratings       float64 `json:"ratings"`
			HD            bool    `json:"hd"`
			UploadDate    string  `json:"upload_date"`

			FeatureDetails struct {
				Title     string  `json:"title"`
				MovieName string  `json:"movie_name"`
				Year      flexInt `json:"year"`
			} `json:"feature_details"`

			Files []struct {
				FileID   int    `json:"file_id"`
				FileName string `json:"file_name"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

// downloadResponse is the /download payload shape.
type downloadResponse struct {
	Link     string `json:"link"`
	FileName string `json:"file_name"`
	Message  string `json:"message"`
}

// flexInt accepts a year given as either a JSON number or a JSON string,
// tolerating either without failing the whole decode — cheap insurance since
// this field cannot be checked against the live API without an account.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil // an unexpected shape here should not sink the whole result
	}
	*f = flexInt(n)
	return nil
}

func (f flexInt) String() string {
	if f == 0 {
		return ""
	}
	return strconv.Itoa(int(f))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

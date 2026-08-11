package api

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// The API returns every scalar as a JSON string, including numbers and the
// boolean-ish is_tvseries / enable_download flags, so the fields below are
// typed as strings rather than fought with.

// Movie is one entry in a listing, search result, slider or genre page. Every
// list endpoint returns this same shape.
type Movie struct {
	ID           string `json:"videos_id"`
	Title        string `json:"title"`
	IMDBRating   string `json:"imdb_rating"`
	Slug         string `json:"slug"`
	Release      string `json:"release"`
	Runtime      string `json:"runtime"`
	Writer       string `json:"writer"` // the localized title, despite the name
	IsTVSeries   string `json:"is_tvseries"`
	VideoQuality string `json:"video_quality"`
	ThumbnailURL string `json:"thumbnail_url"`
	PosterURL    string `json:"poster_url"`
	Description  string `json:"description"`
}

// IsSeries reports whether this entry is a TV series rather than a film.
func (m Movie) IsSeries() bool { return m.IsTVSeries == "1" }

// Kind is the `type` parameter get_single_details expects for this entry.
func (m Movie) Kind() string {
	if m.IsSeries() {
		return "tvseries"
	}
	return "movie"
}

// KindLabel is the human-readable form for the table.
func (m Movie) KindLabel() string {
	if m.IsSeries() {
		return "Series"
	}
	return "Movie"
}

// Year is the release year, tolerating both "2016-12-16" and "2016".
func (m Movie) Year() string {
	if len(m.Release) >= 4 {
		return m.Release[:4]
	}
	return m.Release
}

// DownloadLink is one downloadable file for a title.
//
// The download URLs point at separate file hosts and are signed with an
// `expires` timestamp, so they go stale. Fetch the details again rather than
// holding on to a link.
type DownloadLink struct {
	ID    string `json:"download_link_id"`
	Label string `json:"label"`
	// Resolution is decorative in this API — every row carries the same
	// download glyph rather than a real resolution. The quality is in Label.
	Resolution string `json:"resolution"`
	// FileSize is a bare number of megabytes, and is frequently null.
	FileSize string `json:"file_size"`
	URL      string `json:"download_url"`
}

// SizeLabel renders FileSize, which the API gives in megabytes, blank when it
// was null or unparseable.
func (d DownloadLink) SizeLabel() string {
	mb, err := strconv.ParseFloat(strings.TrimSpace(d.FileSize), 64)
	if err != nil || mb <= 0 {
		return ""
	}
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// Ext is the file extension implied by the download URL, which carries a query
// string that must not be mistaken for one. Defaults to .mp4.
func (d DownloadLink) Ext() string {
	urlPath := d.URL
	if i := strings.IndexByte(urlPath, '?'); i >= 0 {
		urlPath = urlPath[:i]
	}
	// path, not path/filepath: this is a URL, and filepath's separator rules
	// differ between the Linux test run and the Windows binary.
	ext := strings.ToLower(path.Ext(urlPath))
	if ext == "" || len(ext) > 5 {
		return ".mp4"
	}
	return ext
}

// Describe renders a link for the picker.
func (d DownloadLink) Describe() string {
	var parts []string
	if label := strings.TrimSpace(d.Label); label != "" {
		parts = append(parts, label)
	}
	// Keep Resolution only when it actually says something; this API fills it
	// with a glyph, but a sibling deployment may not.
	if res := strings.TrimSpace(d.Resolution); res != "" && strings.ContainsAny(res, "0123456789") {
		parts = append(parts, res)
	}
	if size := d.SizeLabel(); size != "" {
		parts = append(parts, size)
	}
	if ext := d.Ext(); ext != "" {
		parts = append(parts, strings.TrimPrefix(ext, "."))
	}
	if len(parts) == 0 {
		return d.URL
	}
	return strings.Join(parts, " · ")
}

// VideoFile is a streamable source for a title.
type VideoFile struct {
	ID       string `json:"video_file_id"`
	Label    string `json:"label"`
	FileType string `json:"file_type"`
	FileSize string `json:"file_size"`
	URL      string `json:"file_url"`
}

// Named is a genre, country, director or cast member — all four share a shape.
type Named struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Detail is the get_single_details payload.
type Detail struct {
	ID             string         `json:"videos_id"`
	Title          string         `json:"title"`
	IMDBRating     string         `json:"imdb_rating"`
	IMDBID         string         `json:"imdbid"`
	Description    string         `json:"description"`
	Release        string         `json:"release"`
	Runtime        string         `json:"runtime"`
	VideoQuality   string         `json:"video_quality"`
	IsTVSeries     string         `json:"is_tvseries"`
	EnableDownload string         `json:"enable_download"`
	Writer         string         `json:"writer"`
	ThumbnailURL   string         `json:"thumbnail_url"`
	PosterURL      string         `json:"poster_url"`
	DownloadLinks  []DownloadLink `json:"download_links"`
	Videos         []VideoFile    `json:"videos"`
	Genre          []Named        `json:"genre"`
	Country        []Named        `json:"country"`
	Director       []Named        `json:"director"`
	Cast           []Named        `json:"cast"`
	RelatedMovie   []Movie        `json:"related_movie"`
}

// DownloadsEnabled reports whether the API offers download links for this title.
func (d Detail) DownloadsEnabled() bool { return d.EnableDownload == "1" }

// searchResponse is the shape of the /search endpoint, which splits results by
// category instead of returning a flat list.
type searchResponse struct {
	Movie      []Movie `json:"movie"`
	TVSeries   []Movie `json:"tvseries"`
	TVChannels []Movie `json:"tv_channels"`
}

// apiError is the envelope the API uses to report a problem. It arrives with
// HTTP 200, so the status code alone never reveals a failure.
type apiError struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// checkAPIError returns a non-nil error when the body is an error envelope
// rather than real data.
func checkAPIError(body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Errorf("empty response")
	}
	// Only an object can be the envelope; list endpoints return an array.
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	var e apiError
	if err := json.Unmarshal(body, &e); err != nil {
		return nil // not the envelope; let the real decode report the problem
	}
	if strings.EqualFold(e.Status, "error") {
		if e.Message != "" {
			return fmt.Errorf("%s", e.Message)
		}
		return fmt.Errorf("the API reported an error")
	}
	return nil
}

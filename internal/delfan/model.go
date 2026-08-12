package delfan

import (
	"strings"
)

// Item is one movie or series in a search result or a home-page row. The API
// returns every scalar as a string, like the playstore API.
type Item struct {
	ID           string `json:"videos_id"`
	Title        string `json:"title"`
	Year         string `json:"year"`
	IMDB         string `json:"imdb"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// DownloadLink is one quality option for a title. Link is a play.php URL that
// 302-redirects to the real, signed and expiring file URL; ResolveDownloadURL
// follows that redirect.
type DownloadLink struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`       // e.g. "کیفیت 720 زیرنویس" (720 with subtitle)
	Link  string `json:"link"`       // play.php resolver
	Size  string `json:"video_size"` // e.g. "809 مگابایت"
	Subno string `json:"subtitle"`
}

// Describe renders a link for the UI.
func (d DownloadLink) Describe() string {
	parts := []string{}
	if n := strings.TrimSpace(d.Name); n != "" {
		parts = append(parts, n)
	}
	if s := strings.TrimSpace(d.Size); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return d.Link
	}
	return strings.Join(parts, " · ")
}

// Detail is one title's full record from the detials endpoint.
type Detail struct {
	ID            string
	Title         string
	OriginalTitle string // the English title, parsed out of Description when present
	IMDBRating    string
	Description   string
	Year          string
	PosterURL     string
	ThumbnailURL  string
	TrailerURL    string
	IsMovie       bool
	DownloadLinks []DownloadLink
}

// Home is the vitrin (home page) payload, trimmed to the rows worth showing.
type Home struct {
	NewMovie     []Item
	NewSerie     []Item
	UpdatedSerie []Item
	Featured     []Item // "vige"
}

// --- raw response envelopes -------------------------------------------------

type loginResponse struct {
	NightMode string `json:"night_mode"`
	Q1        any    `json:"q1"`
	Q2        any    `json:"q2"`
	Infos     []struct {
		Auth  string `json:"auth"`
		Login string `json:"login"`
	} `json:"infos"`
}

type vitrinResponse struct {
	StateAll     string `json:"state_all"`
	Q1           any    `json:"q1"`
	Q2           any    `json:"q2"`
	NewMovie     []Item `json:"NewMovie"`
	NewSerie     []Item `json:"NewSerie"`
	UpdatedSerie []Item `json:"updated_serie"`
	Vige         []Item `json:"vige"`
}

type searchResponse struct {
	StateAll string `json:"state_all"`
	Msg      string `json:"msg"`
	Q1       any    `json:"q1"`
	Q2       any    `json:"q2"`
	All      []Item `json:"all"`
}

type detialsResponse struct {
	StateAll string `json:"state_all"`
	Msg      string `json:"msg"`
	Q1       any    `json:"q1"`
	Q2       any    `json:"q2"`
	Detiles  []struct {
		ID           any            `json:"id"`
		Title        string         `json:"title"`
		IMDBRating   string         `json:"imdb_rating"`
		Description  string         `json:"description"`
		Year         string         `json:"year"`
		PosterURL    string         `json:"poster_url"`
		ThumbnailURL string         `json:"thumbnail_url"`
		TrailerURL   string         `json:"trailer_url"`
		IsMovie      string         `json:"is_movie"`
		DownloadLink []DownloadLink `json:"download_link"`
	} `json:"detiles"`
}

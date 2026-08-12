package delfan

import (
	"encoding/json"
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
	// ID is a JSON number for film qualities but a JSON string for episodes, so
	// it is decoded loosely.
	ID    any    `json:"id"`
	Name  string `json:"name"`       // e.g. "کیفیت 720 زیرنویس" (720 with subtitle) or "قسمت 1"
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

// Season groups a series' episodes (one entry per language/quality cut, e.g.
// "فصل 1 زیرنویس" = season 1, subtitled).
type Season struct {
	Name     string
	Episodes []DownloadLink
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
	DownloadLinks []DownloadLink // films only
	Seasons       []Season       // series only
}

// Cast is a person (actor/director) returned by a cast search.
type Cast struct {
	ID   string
	Name string
	Role string // action_user, e.g. "بازیگر" (actor)
	Pic  string
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
		ID           any    `json:"id"`
		Title        string `json:"title"`
		IMDBRating   string `json:"imdb_rating"`
		Description  string `json:"description"`
		Year         string `json:"year"`
		PosterURL    string `json:"poster_url"`
		ThumbnailURL string `json:"thumbnail_url"`
		TrailerURL   string `json:"trailer_url"`
		IsMovie      string `json:"is_movie"`
		// download_link is polymorphic: for a film it is a flat array of
		// DownloadLink; for a series it is an array of {session_title, links}.
		// Decoded conditionally in Client.Details.
		DownloadLink json.RawMessage `json:"download_link"`
	} `json:"detiles"`
}

// seriesSeason is the series shape of a download_link entry.
type seriesSeason struct {
	SessionTitle string         `json:"session_title"`
	Links        []DownloadLink `json:"links"`
}

type searchCastResponse struct {
	StateAll  string `json:"state_all"`
	Msg       string `json:"msg"`
	Q1        any    `json:"q1"`
	Q2        any    `json:"q2"`
	MovieList []struct {
		ID         any    `json:"id"`
		Name       string `json:"name"`
		ActionUser string `json:"action_user"`
		PicURL     string `json:"pic_url"`
	} `json:"movie_list"`
}

type castMoviesResponse struct {
	StateAll  string `json:"state_all"`
	Msg       string `json:"msg"`
	Q1        any    `json:"q1"`
	Q2        any    `json:"q2"`
	Bio       string `json:"bio"`
	MovieList []Item `json:"movie_list"`
}

package ui

import (
	"strconv"

	"moviefinder/internal/api"
	"moviefinder/internal/delfan"
)

// The Delfan source produces its own types; these converters map them onto the
// api.Movie / api.Detail shapes the poster grid and detail pane already render,
// so the whole UI stays source-agnostic.

func delfanItemToMovie(it delfan.Item) api.Movie {
	return api.Movie{
		ID:           it.ID,
		Title:        it.Title,
		IMDBRating:   it.IMDB,
		Release:      it.Year,
		ThumbnailURL: it.ThumbnailURL,
		PosterURL:    it.ThumbnailURL,
		// Delfan search results do not distinguish movie vs series, and its
		// detail lookup does not need a kind, so leave IsTVSeries at its zero
		// value (renders as "Movie").
	}
}

func delfanItemsToMovies(items []delfan.Item) []api.Movie {
	movies := make([]api.Movie, 0, len(items))
	for _, it := range items {
		movies = append(movies, delfanItemToMovie(it))
	}
	return movies
}

// delfanDetailToDetail maps a Delfan detail onto api.Detail.
//
// For films, resolved holds each link's real file URL (parallel to
// d.DownloadLinks); an unresolved entry keeps its play.php URL, which still
// 302-redirects to the file when opened. For series, the episode play.php URLs
// are kept as-is — resolving every episode upfront would be dozens of requests,
// and they redirect on play just the same.
func delfanDetailToDetail(d delfan.Detail, resolved []string) api.Detail {
	detail := api.Detail{
		ID:           d.ID,
		Title:        d.Title,
		IMDBRating:   d.IMDBRating,
		Description:  d.Description,
		Release:      d.Year,
		PosterURL:    d.PosterURL,
		ThumbnailURL: d.ThumbnailURL,
	}

	for i, l := range d.DownloadLinks {
		url := l.Link
		if i < len(resolved) && resolved[i] != "" {
			url = resolved[i]
		}
		detail.DownloadLinks = append(detail.DownloadLinks, api.DownloadLink{
			ID: anyToStr(l.ID), Label: l.Describe(), URL: url,
		})
	}

	for _, s := range d.Seasons {
		eps := make([]api.DownloadLink, 0, len(s.Episodes))
		for _, e := range s.Episodes {
			eps = append(eps, api.DownloadLink{ID: anyToStr(e.ID), Label: e.Describe(), URL: e.Link})
		}
		if len(eps) > 0 {
			detail.Seasons = append(detail.Seasons, api.Season{Name: s.Name, Episodes: eps})
		}
	}
	return detail
}

// anyToStr renders a JSON value that may be a number or a string as a string.
func anyToStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

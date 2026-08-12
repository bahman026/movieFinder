package ui

import (
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

// delfanDetailToDetail maps a Delfan detail onto api.Detail. resolved holds each
// link's real file URL (parallel to d.DownloadLinks); where a link could not be
// resolved, the play.php URL is kept as a fallback, since it still 302-redirects
// to the file when opened.
func delfanDetailToDetail(d delfan.Detail, resolved []string) api.Detail {
	links := make([]api.DownloadLink, 0, len(d.DownloadLinks))
	for i, l := range d.DownloadLinks {
		url := l.Link
		if i < len(resolved) && resolved[i] != "" {
			url = resolved[i]
		}
		links = append(links, api.DownloadLink{
			ID:    itoa(l.ID),
			Label: l.Describe(),
			URL:   url,
		})
	}
	return api.Detail{
		ID:            d.ID,
		Title:         d.Title,
		IMDBRating:    d.IMDBRating,
		Description:   d.Description,
		Release:       d.Year,
		PosterURL:     d.PosterURL,
		ThumbnailURL:  d.ThumbnailURL,
		DownloadLinks: links,
	}
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	// small positive ids; avoid importing strconv for one call site elsewhere
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

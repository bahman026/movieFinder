package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/mysubs"
	"moviefinder/internal/opensubtitles"
)

// The two subtitle sources. MySubs is the default because OpenSubtitles caps
// anonymous downloads at a handful per day and my-subs.co has no such quota;
// OpenSubtitles is kept because it matches by IMDb id, which is far more
// precise than my-subs.co's title search.
const (
	providerMySubs        = "mysubs"
	providerOpenSubtitles = "opensubtitles"
)

var subtitleSources = []struct{ code, label string }{
	{providerMySubs, "MySubs (no daily limit)"},
	{providerOpenSubtitles, "OpenSubtitles (~5/day)"},
}

func sourceLabels() []string {
	labels := make([]string, len(subtitleSources))
	for i, s := range subtitleSources {
		labels[i] = s.label
	}
	return labels
}

func sourceLabel(code string) string {
	for _, s := range subtitleSources {
		if s.code == code {
			return s.label
		}
	}
	return subtitleSources[0].label
}

func sourceCode(label string) string {
	for _, s := range subtitleSources {
		if s.label == label {
			return s.code
		}
	}
	return providerMySubs
}

// subtitleQuery is one search, in whichever terms the chosen source can use.
type subtitleQuery struct {
	title   string
	imdbID  string
	year    string
	lang    string
	season  int
	episode int
}

// subtitleHit is one result, flattened from whichever source produced it so a
// single row widget renders both. download is a closure rather than an id
// because the two sources need different things to fetch a file.
type subtitleHit struct {
	title    string
	release  string
	meta     []string
	download func(ctx context.Context) (data []byte, fileName string, err error)
}

// subtitleSearch runs one query against one source.
type subtitleSearch func(ctx context.Context, q subtitleQuery) ([]subtitleHit, error)

// subtitleSearcher resolves a source name to its search function. It must be
// called on the UI thread and the result used from the goroutine, not the
// other way round: saving Settings replaces both clients, so the pointer has
// to be read where that write happens rather than off in a search goroutine.
func (u *UI) subtitleSearcher(source string) subtitleSearch {
	if source == providerOpenSubtitles {
		client := u.subtitles
		return func(ctx context.Context, q subtitleQuery) ([]subtitleHit, error) {
			return searchOpenSubtitles(ctx, client, q)
		}
	}
	client := u.mysubs
	return func(ctx context.Context, q subtitleQuery) ([]subtitleHit, error) {
		return searchMySubs(ctx, client, q)
	}
}

func searchMySubs(ctx context.Context, client *mysubs.Client, q subtitleQuery) ([]subtitleHit, error) {
	subs, err := client.Search(ctx, mysubs.Query{
		Title:    q.title,
		Year:     q.year,
		Language: q.lang,
		Season:   q.season,
		Episode:  q.episode,
	})
	if err != nil {
		return nil, err
	}

	hits := make([]subtitleHit, 0, len(subs))
	for _, sub := range subs {
		sub := sub
		title := sub.Title
		if title == "" {
			title = q.title
		}
		if sub.Year != "" {
			title += " (" + sub.Year + ")"
		}

		meta := []string{sub.Language, fmt.Sprintf("%d downloads", sub.DownloadCount)}
		if sub.Uploader != "" {
			by := "by " + sub.Uploader
			if sub.Age != "" {
				by += " · " + sub.Age
			}
			meta = append(meta, by)
		}

		hits = append(hits, subtitleHit{
			title:   title,
			release: sub.Release,
			meta:    meta,
			download: func(ctx context.Context) ([]byte, string, error) {
				return client.Download(ctx, sub.ID)
			},
		})
	}
	return hits, nil
}

func searchOpenSubtitles(ctx context.Context, client *opensubtitles.Client, q subtitleQuery) ([]subtitleHit, error) {
	subs, err := client.SearchEpisode(ctx, q.title, q.imdbID, q.year, q.lang, q.season, q.episode)
	if err != nil {
		return nil, err
	}

	hits := make([]subtitleHit, 0, len(subs))
	for _, sub := range subs {
		sub := sub
		title := sub.MovieName
		if title == "" {
			title = "(unknown title)"
		}
		if sub.Year != "" {
			title += " (" + sub.Year + ")"
		}

		var meta []string
		if uploaded := sub.UploadDateLabel(); uploaded != "" {
			meta = append(meta, "Uploaded "+uploaded)
		}
		meta = append(meta, fmt.Sprintf("%d downloads", sub.DownloadCount))
		if sub.Rating > 0 {
			meta = append(meta, fmt.Sprintf("%.1f rating", sub.Rating))
		}
		if sub.HD {
			meta = append(meta, "HD")
		}

		fileID := sub.FileID
		hits = append(hits, subtitleHit{
			title:   title,
			release: sub.Release,
			meta:    meta,
			download: func(ctx context.Context) ([]byte, string, error) {
				return client.Download(ctx, fileID)
			},
		})
	}
	return hits, nil
}

// sourceFailureMessage explains a subtitle-source outage without implying the
// app is broken. These are separate third-party sites: one being down says
// nothing about the movie servers, and browsing, downloading and playback
// carry on regardless.
func sourceFailureMessage(source string, err error) string {
	return source + " could not be reached: " + firstLine(err.Error()) +
		". Try the other source — browsing and downloads do not depend on it."
}

// playSourceFailureMessage is the same for the Play dialog, where the useful
// next step is to start the film without a subtitle.
func playSourceFailureMessage(source string, err error) string {
	return source + " could not be reached: " + firstLine(err.Error()) +
		". Try the other source, or play without a subtitle."
}

// subtitleControls is the source/language/episode row shared by the Subtitles
// dialog and the Play dialog, so the two cannot drift apart.
type subtitleControls struct {
	source   *widget.Select
	language *widget.Select
	season   *widget.Entry
	episode  *widget.Entry

	// widget is the assembled row to drop into a dialog header.
	widget fyne.CanvasObject
}

// newSubtitleControls builds the row. onSearch runs whenever a choice changes
// — except for the season and episode boxes, which fire on Enter or the
// Search button instead, so typing "12" does not launch two searches.
func (u *UI) newSubtitleControls(onSearch func()) *subtitleControls {
	c := &subtitleControls{
		source:   widget.NewSelect(sourceLabels(), nil),
		language: widget.NewSelect(languageLabels(), nil),
		season:   widget.NewEntry(),
		episode:  widget.NewEntry(),
	}
	c.source.SetSelected(sourceLabel(u.cfg.SubtitleProvider))
	c.language.SetSelected(languageLabel("en"))
	c.season.SetPlaceHolder("1")
	c.episode.SetPlaceHolder("1")

	c.source.OnChanged = func(string) { onSearch() }
	c.language.OnChanged = func(string) { onSearch() }
	c.season.OnSubmitted = func(string) { onSearch() }
	c.episode.OnSubmitted = func(string) { onSearch() }

	searchButton := widget.NewButtonWithIcon("Search", theme.SearchIcon(), onSearch)

	c.widget = container.NewVBox(
		container.NewGridWithColumns(2,
			container.NewBorder(nil, nil, widget.NewLabel("Source:"), nil, c.source),
			container.NewBorder(nil, nil, widget.NewLabel("Language:"), nil, c.language),
		),
		container.NewBorder(nil, nil,
			widget.NewLabel("TV series:"),
			searchButton,
			container.NewGridWithColumns(4,
				widget.NewLabel("Season"), c.season,
				widget.NewLabel("Episode"), c.episode,
			),
		),
	)
	return c
}

func (c *subtitleControls) sourceCode() string { return sourceCode(c.source.Selected) }

// query folds the current title metadata and the control values into one
// search. Season and episode are used only when both are filled in — one of
// the two on its own cannot name an episode.
func (c *subtitleControls) query(title, imdbID, year string) subtitleQuery {
	q := subtitleQuery{
		title:  title,
		imdbID: imdbID,
		year:   year,
		lang:   languageCode(c.language.Selected),
	}
	season := positiveNumber(c.season.Text)
	episode := positiveNumber(c.episode.Text)
	if season > 0 && episode > 0 {
		q.season, q.episode = season, episode
	}
	return q
}

func positiveNumber(text string) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

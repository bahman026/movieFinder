// Package ui builds the desktop window.
package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/adlas/moviefinder/internal/api"
	"github.com/adlas/moviefinder/internal/config"
	"github.com/adlas/moviefinder/internal/opensubtitles"
)

const appID = "at.adlas.moviefinder"

// UI owns the window and the state shown in it.
type UI struct {
	app    fyne.App
	window fyne.Window

	cfg       config.Config
	client    *api.Client
	subtitles *opensubtitles.Client
	images    *imageCache

	// mu guards everything the loader goroutines write and the widget
	// callbacks read on the UI thread.
	mu       sync.RWMutex
	movies   []api.Movie
	detail   api.Detail
	links    []api.DownloadLink
	page     int
	query    string
	selected int

	// cancelLoad stops the in-flight listing request when a new one starts;
	// cancelDetail does the same for the detail pane.
	cancelLoad   context.CancelFunc
	cancelDetail context.CancelFunc

	grid       *widget.GridWrap
	search     *widget.Entry
	status     *widget.Label
	hostLabel  *widget.Label
	info       *widget.RichText
	linkBox    *fyne.Container
	pageLabel  *widget.Label
	prevButton *widget.Button
	nextButton *widget.Button
}

// Run builds the window and blocks until it is closed.
func Run() {
	cfg, err := config.Load()

	a := fyneapp.NewWithID(appID)
	w := a.NewWindow("MovieFinder")
	w.Resize(fyne.NewSize(1180, 760))

	u := &UI{
		app:       a,
		window:    w,
		cfg:       cfg,
		client:    api.New(cfg),
		subtitles: opensubtitles.New(cfg.OpenSubtitlesAPIKey),
		images:    newImageCache(),
		page:      1,
		selected:  -1,
	}
	w.SetContent(u.build())
	w.SetMaster()

	if err != nil {
		u.setStatus("Could not read settings: " + err.Error())
	}
	u.reload(1)

	w.ShowAndRun()
}

func (u *UI) build() fyne.CanvasObject {
	u.search = widget.NewEntry()
	u.search.SetPlaceHolder("Search movies and series, or leave empty to browse")
	u.search.OnSubmitted = func(q string) { u.setQuery(q) }

	searchButton := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {
		u.setQuery(u.search.Text)
	})
	clearButton := widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
		u.search.SetText("")
		u.setQuery("")
	})
	refreshButton := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		u.reload(u.currentPage())
	})
	settingsButton := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() {
		u.showSettings()
	})

	toolbar := container.NewBorder(nil, nil, nil,
		container.NewHBox(searchButton, clearButton, refreshButton, settingsButton),
		u.search,
	)

	u.grid = u.buildGrid()
	split := container.NewHSplit(u.grid, u.buildDetailPane())
	split.Offset = 0.63

	u.status = widget.NewLabel("Loading…")
	u.hostLabel = widget.NewLabel("")

	u.pageLabel = widget.NewLabel("Page 1")
	u.prevButton = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if p := u.currentPage(); p > 1 {
			u.reload(p - 1)
		}
	})
	u.nextButton = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		u.reload(u.currentPage() + 1)
	})
	u.prevButton.Disable()

	statusBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(u.prevButton, u.pageLabel, u.nextButton),
		container.NewVBox(u.status, u.hostLabel),
	)

	return container.NewBorder(toolbar, statusBar, nil, nil, split)
}

func (u *UI) buildGrid() *widget.GridWrap {
	grid := widget.NewGridWrap(
		func() int {
			u.mu.RLock()
			defer u.mu.RUnlock()
			return len(u.movies)
		},
		func() fyne.CanvasObject {
			return newPosterCard()
		},
		func(id widget.GridWrapItemID, item fyne.CanvasObject) {
			movie, ok := u.movieAt(int(id))
			if !ok {
				return
			}
			if card, ok := item.(*posterCard); ok {
				card.set(movie, u.loadPoster)
			}
		},
	)
	grid.OnSelected = func(id widget.GridWrapItemID) {
		u.mu.Lock()
		u.selected = int(id)
		u.mu.Unlock()
		u.loadDetail(int(id))
	}
	return grid
}

// loadPoster serves a poster from cache, or fetches it in the background.
func (u *UI) loadPoster(url string, apply func(fyne.Resource)) {
	if res, ok := u.images.get(url); ok {
		apply(res)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		data, err := u.client.Image(ctx, url)
		if err != nil {
			return // a missing poster is not worth interrupting the user for
		}
		res := fyne.NewStaticResource(url, data)
		u.images.put(url, res)

		fyne.Do(func() { apply(res) })
	}()
}

func (u *UI) buildDetailPane() fyne.CanvasObject {
	u.info = widget.NewRichTextFromMarkdown("_Select a title to see its links._")
	u.info.Wrapping = fyne.TextWrapWord

	subtitlesButton := widget.NewButtonWithIcon("Find Subtitles", theme.SearchIcon(), func() {
		u.showSubtitles()
	})

	u.linkBox = container.NewVBox()

	content := container.NewVBox(
		u.info,
		subtitlesButton,
		widget.NewSeparator(),
		u.linkBox,
	)

	return container.NewBorder(
		widget.NewLabelWithStyle("Details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		container.NewVScroll(content),
	)
}

// setQuery switches between browsing and searching.
func (u *UI) setQuery(query string) {
	u.mu.Lock()
	u.query = strings.TrimSpace(query)
	u.mu.Unlock()
	u.reload(1)
}

// reload fetches a listing or a search in the background and swaps it in.
func (u *UI) reload(page int) {
	if u.cancelLoad != nil {
		u.cancelLoad()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelLoad = cancel

	u.mu.RLock()
	query := u.query
	u.mu.RUnlock()

	searching := query != ""
	if searching {
		page = 1 // the search endpoint is not paged
		u.setStatus("Searching for " + query + "…")
	} else {
		u.setStatus(fmt.Sprintf("Loading page %d…", page))
	}

	go func() {
		var (
			movies []api.Movie
			err    error
		)
		if searching {
			movies, err = u.client.Search(ctx, query)
		} else {
			movies, err = u.client.Movies(ctx, page)
		}
		if ctx.Err() != nil {
			return // superseded by a newer request
		}

		fyne.Do(func() {
			u.hostLabel.SetText("Server: " + u.client.ActiveHost())
			if err != nil {
				u.setStatus("Failed: " + firstLine(err.Error()))
				dialog.ShowError(err, u.window)
				return
			}

			u.mu.Lock()
			u.movies = movies
			u.page = page
			u.selected = -1
			u.mu.Unlock()

			u.grid.UnselectAll()
			u.grid.Refresh()
			u.grid.ScrollToTop()
			u.clearDetail("_Select a title to see its links._")

			u.pageLabel.SetText(fmt.Sprintf("Page %d", page))
			switch {
			case searching:
				// The search endpoint returns everything at once.
				u.prevButton.Disable()
				u.nextButton.Disable()
			default:
				if page > 1 {
					u.prevButton.Enable()
				} else {
					u.prevButton.Disable()
				}
				// An empty page means we have run past the end.
				if len(movies) == 0 {
					u.nextButton.Disable()
				} else {
					u.nextButton.Enable()
				}
			}

			switch {
			case len(movies) == 0 && searching:
				u.setStatus("Nothing found for " + query + ".")
			case len(movies) == 0:
				u.setStatus("No titles on this page.")
			case searching:
				u.setStatus(fmt.Sprintf("%d result(s) for %s.", len(movies), query))
			default:
				u.setStatus(fmt.Sprintf("%d title(s) on page %d.", len(movies), page))
			}
		})
	}()
}

// loadDetail fetches the full record for a card and fills the detail pane.
func (u *UI) loadDetail(index int) {
	movie, ok := u.movieAt(index)
	if !ok {
		return
	}

	if u.cancelDetail != nil {
		u.cancelDetail()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelDetail = cancel

	u.clearDetail("## " + movie.Title + "\n\n_Loading…_")

	go func() {
		detail, err := u.client.Details(ctx, movie.Kind(), movie.ID)
		if ctx.Err() != nil {
			return
		}

		fyne.Do(func() {
			if err != nil {
				u.info.ParseMarkdown("## " + movie.Title + "\n\nCould not load details: " + err.Error())
				return
			}

			u.mu.Lock()
			u.detail = detail
			u.links = detail.DownloadLinks
			u.mu.Unlock()

			u.info.ParseMarkdown(renderDetail(detail))
			u.showLinks(detail.DownloadLinks)
		})
	}()
}

// showLinks lists every link with its URL and a copy button.
func (u *UI) showLinks(links []api.DownloadLink) {
	u.linkBox.RemoveAll()

	if len(links) == 0 {
		u.linkBox.Add(widget.NewLabel("No links for this title."))
		u.linkBox.Refresh()
		return
	}

	header := widget.NewLabelWithStyle(
		fmt.Sprintf("Links (%d)", len(links)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.linkBox.Add(header)

	for _, link := range links {
		link := link

		label := widget.NewLabel(link.Describe())
		label.TextStyle = fyne.TextStyle{Bold: true}

		// A disabled entry rather than a label, so the URL stays selectable
		// for copying by hand while being uneditable.
		field := widget.NewEntry()
		field.SetText(link.URL)
		field.Wrapping = fyne.TextWrapOff
		field.Disable()

		copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
			u.window.Clipboard().SetContent(link.URL)
			u.setStatus("Copied: " + link.Describe())
		})

		u.linkBox.Add(container.NewVBox(
			container.NewBorder(nil, nil, nil, copyButton, label),
			field,
			widget.NewSeparator(),
		))
	}
	u.linkBox.Refresh()
}

func renderDetail(d api.Detail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", d.Title)
	if d.Writer != "" {
		fmt.Fprintf(&b, "%s\n\n", d.Writer)
	}

	writeField(&b, "Released", d.Release)
	writeField(&b, "Runtime", d.Runtime)
	writeField(&b, "IMDb", strings.TrimSpace(d.IMDBRating+" ("+d.IMDBID+")"))
	writeField(&b, "Quality", d.VideoQuality)
	writeField(&b, "Genre", joinNames(d.Genre))
	writeField(&b, "Country", joinNames(d.Country))
	writeField(&b, "Director", joinNames(d.Director))
	if cast := joinNames(d.Cast); cast != "" {
		writeField(&b, "Cast", truncate(cast, 300))
	}

	if d.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", d.Description)
	}
	return b.String()
}

func (u *UI) clearDetail(markdown string) {
	u.info.ParseMarkdown(markdown)
	u.mu.Lock()
	u.links = nil
	u.detail = api.Detail{}
	u.mu.Unlock()
	u.linkBox.RemoveAll()
	u.linkBox.Refresh()
}

func (u *UI) movieAt(index int) (api.Movie, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if index < 0 || index >= len(u.movies) {
		return api.Movie{}, false
	}
	return u.movies[index], true
}

func (u *UI) currentPage() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.page < 1 {
		return 1
	}
	return u.page
}

func (u *UI) setStatus(msg string) {
	if u.status != nil {
		u.status.SetText(msg)
	}
}

func writeField(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "()" {
		return
	}
	fmt.Fprintf(b, "- **%s:** %s\n", label, value)
}

func joinNames(items []api.Named) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) != "" {
			names = append(names, item.Name)
		}
	}
	return strings.Join(names, ", ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

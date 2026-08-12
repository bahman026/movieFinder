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

	"moviefinder/internal/api"
	"moviefinder/internal/config"
	"moviefinder/internal/delfan"
	"moviefinder/internal/opensubtitles"
	"moviefinder/internal/stream"
)

// Sources the user can switch between.
const (
	sourceMovieFinder = "MovieFinder"
	sourceDelfan      = "Delfan"
)

const appID = "com.moviefinder.app"

// UI owns the window and the state shown in it.
type UI struct {
	app    fyne.App
	window fyne.Window

	cfg       config.Config
	client    *api.Client
	delfan    *delfan.Client
	subtitles *opensubtitles.Client
	images    *imageCache

	// mu guards everything the loader goroutines write and the widget
	// callbacks read on the UI thread.
	mu       sync.RWMutex
	source   string
	movies   []api.Movie
	detail   api.Detail
	links    []api.DownloadLink
	page     int
	query    string
	selected int
	// subtitle-search metadata for the current detail, kept separate so the
	// Delfan source can search OpenSubtitles by its English original title
	// rather than the Persian display title.
	subTitle string
	subIMDB  string
	subYear  string

	// cancelLoad stops the in-flight listing request when a new one starts;
	// cancelDetail does the same for the detail pane.
	cancelLoad   context.CancelFunc
	cancelDetail context.CancelFunc

	grid       *widget.GridWrap
	search     *widget.Entry
	status     *widget.Label
	hostLabel  *widget.Label
	info       *widget.Entry
	infoText   string // the intended details text, so edits can be reverted
	imdbLink   *widget.Hyperlink
	linkBox    *fyne.Container
	pageLabel   *widget.Label
	prevButton  *widget.Button
	nextButton  *widget.Button
	playDialog  dialog.Dialog
	dlControls  *fyne.Container
	pauseButton *widget.Button
	cancelBtn   *widget.Button

	// activeStream is the current download-while-playing server, if any. A new
	// playback stops the previous one.
	activeStream *stream.Server
}

// Run builds the window and blocks until it is closed.
func Run() {
	cfg, err := config.Load()

	a := fyneapp.NewWithID(appID)
	a.SetIcon(appIcon)
	w := a.NewWindow("MovieFinder")
	w.SetIcon(appIcon)
	w.Resize(fyne.NewSize(1180, 760))

	u := &UI{
		app:       a,
		window:    w,
		cfg:       cfg,
		client:    api.New(cfg),
		delfan:    delfan.New(cfg.DelfanLoginHost, cfg.DelfanAPIHost),
		subtitles: opensubtitles.New(opensubtitles.ResolveKey(cfg.OpenSubtitlesAPIKey)),
		images:    newImageCache(),
		source:    sourceMovieFinder,
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
	searchButton.Importance = widget.HighImportance
	clearButton := widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
		u.search.SetText("")
		u.setQuery("")
	})
	refreshButton := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		u.reload(u.currentPage())
	})
	settingsButton := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		u.showSettings()
	})

	sourceSelect := widget.NewSelect([]string{sourceMovieFinder, sourceDelfan}, func(name string) {
		u.mu.Lock()
		u.source = name
		u.query = ""
		u.mu.Unlock()
		u.search.SetText("")
		u.reload(1)
	})
	sourceSelect.SetSelected(sourceMovieFinder)

	// Search box with its search/clear buttons attached on the right, and the
	// source picker + settings on the outer edges — one clean top row.
	searchBox := container.NewBorder(nil, nil, nil,
		container.NewHBox(searchButton, clearButton), u.search)
	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(sourceSelect, refreshButton),
		settingsButton,
		searchBox,
	)
	top := container.NewVBox(container.NewPadded(toolbar), widget.NewSeparator())

	u.grid = u.buildGrid()
	split := container.NewHSplit(u.grid, u.buildDetailPane())
	split.Offset = 0.63

	u.status = widget.NewLabel("Loading…")
	u.hostLabel = widget.NewLabel("")
	u.hostLabel.TextStyle = fyne.TextStyle{Italic: true}

	// Pager: prev / "Page N" / next as one tidy centred group.
	u.pageLabel = widget.NewLabelWithStyle("Page 1", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	u.prevButton = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if p := u.currentPage(); p > 1 {
			u.reload(p - 1)
		}
	})
	u.nextButton = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		u.reload(u.currentPage() + 1)
	})
	u.prevButton.Disable()
	pager := container.NewHBox(u.prevButton, u.pageLabel, u.nextButton)

	// Download controls, shown only while a download-while-playing is active.
	u.pauseButton = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), u.togglePause)
	u.cancelBtn = widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), u.cancelDownload)
	u.cancelBtn.Importance = widget.DangerImportance
	u.dlControls = container.NewHBox(u.pauseButton, u.cancelBtn)
	u.dlControls.Hide()

	statusBar := container.NewBorder(nil, nil,
		container.NewVBox(u.status, u.hostLabel),
		container.NewHBox(u.dlControls, pager),
		nil,
	)
	bottom := container.NewVBox(widget.NewSeparator(), container.NewPadded(statusBar))

	return container.NewBorder(top, bottom, nil, nil, split)
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
	// A multi-line entry (not RichText) so the text can be selected and copied.
	// Kept enabled — not Disable()d — so the text uses the normal readable theme
	// colour rather than the greyed disabled colour; edits are reverted below so
	// it behaves as read-only.
	u.info = widget.NewMultiLineEntry()
	u.info.Wrapping = fyne.TextWrapWord
	u.infoText = "Select a title to see its details."
	u.info.SetText(u.infoText)
	u.info.OnChanged = func(s string) {
		if s != u.infoText {
			u.info.SetText(u.infoText) // read-only: undo any typing
		}
	}

	u.imdbLink = widget.NewHyperlink("", nil)
	u.imdbLink.Hide()

	copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		if text := strings.TrimSpace(u.info.Text); text != "" {
			u.window.Clipboard().SetContent(text)
			u.setStatus("Details copied.")
		}
	})
	copyButton.Importance = widget.LowImportance
	subtitlesButton := widget.NewButtonWithIcon("Subtitles", theme.SearchIcon(), func() {
		u.showSubtitles()
	})

	u.linkBox = container.NewVBox()

	header := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("Details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(u.imdbLink, copyButton, subtitlesButton),
		nil,
	)

	// A split gives the selectable details and the links their own scrollable
	// areas, so a long description does not push the links off-screen.
	body := container.NewVSplit(u.info, container.NewVScroll(u.linkBox))
	body.Offset = 0.55

	return container.NewBorder(container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, body)
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
	source := u.source
	u.mu.RUnlock()

	searching := query != ""
	// Which combinations page: MovieFinder browse and Delfan search are paged;
	// MovieFinder search (returns everything at once) and Delfan browse (a
	// single home-page fetch) are not.
	paged := (source == sourceMovieFinder && !searching) || (source == sourceDelfan && searching)
	if !paged {
		page = 1
	}
	if searching {
		u.setStatus("Searching for " + query + "…")
	} else {
		u.setStatus("Loading…")
	}

	go func() {
		movies, host, err := u.fetch(ctx, source, query, searching, page)
		if ctx.Err() != nil {
			return // superseded by a newer request
		}

		fyne.Do(func() {
			u.hostLabel.SetText("Server: " + host)
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
			u.clearDetail("Select a title to see its details.")

			u.pageLabel.SetText(fmt.Sprintf("Page %d", page))
			if paged {
				if page > 1 {
					u.prevButton.Enable()
				} else {
					u.prevButton.Disable()
				}
				if len(movies) == 0 {
					u.nextButton.Disable() // ran past the end
				} else {
					u.nextButton.Enable()
				}
			} else {
				u.prevButton.Disable()
				u.nextButton.Disable()
			}

			switch {
			case len(movies) == 0 && searching:
				u.setStatus("Nothing found for " + query + ".")
			case len(movies) == 0:
				u.setStatus("No titles to show.")
			case searching:
				u.setStatus(fmt.Sprintf("%d result(s) for %s.", len(movies), query))
			case paged:
				u.setStatus(fmt.Sprintf("%d title(s) on page %d.", len(movies), page))
			default:
				u.setStatus(fmt.Sprintf("%d title(s).", len(movies)))
			}
		})
	}()
}

// fetch pulls a page of titles from the active source and returns the human
// label for the server that answered.
func (u *UI) fetch(ctx context.Context, source, query string, searching bool, page int) ([]api.Movie, string, error) {
	if source == sourceDelfan {
		var (
			items []delfan.Item
			err   error
		)
		if searching {
			items, err = u.delfan.Search(ctx, query, page)
		} else {
			items, err = u.delfan.Home(ctx)
		}
		return delfanItemsToMovies(items), "Delfan", err
	}

	var (
		movies []api.Movie
		err    error
	)
	if searching {
		movies, err = u.client.Search(ctx, query)
	} else {
		movies, err = u.client.Movies(ctx, page)
	}
	return movies, u.client.ActiveHost(), err
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

	u.mu.RLock()
	source := u.source
	u.mu.RUnlock()

	u.clearDetail(movie.Title + "\n\nLoading…")

	go func() {
		detail, sub, err := u.fetchDetail(ctx, source, movie)
		if ctx.Err() != nil {
			return
		}

		fyne.Do(func() {
			if err != nil {
				u.setInfo(movie.Title + "\n\nCould not load details: " + err.Error())
				return
			}

			u.mu.Lock()
			u.detail = detail
			u.links = detail.DownloadLinks
			u.subTitle, u.subIMDB, u.subYear = sub.title, sub.imdb, sub.year
			u.mu.Unlock()

			u.setInfo(renderDetail(detail))
			if link := imdbLink(detail.IMDBID); link != "" {
				u.imdbLink.SetText("View on IMDb")
				_ = u.imdbLink.SetURLFromString(link)
				u.imdbLink.Show()
			} else {
				u.imdbLink.Hide()
			}
			u.showLinks(detail.DownloadLinks)
		})
	}()
}

// subMeta is what the subtitle search needs, kept separate from the display
// detail so Delfan can search by English title while showing the Persian one.
type subMeta struct{ title, imdb, year string }

func (u *UI) fetchDetail(ctx context.Context, source string, movie api.Movie) (api.Detail, subMeta, error) {
	if source == sourceDelfan {
		d, err := u.delfan.Details(ctx, movie.ID)
		if err != nil {
			return api.Detail{}, subMeta{}, err
		}
		// Resolve each play.php link to its real stream/download URL. Bounded
		// so a slow file host cannot hang the detail pane indefinitely.
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		resolved := u.delfan.ResolveLinks(rctx, d.DownloadLinks)
		cancel()

		detail := delfanDetailToDetail(d, resolved)
		title := d.OriginalTitle
		if title == "" {
			title = d.Title
		}
		return detail, subMeta{title: title, imdb: "", year: d.Year}, nil
	}

	detail, err := u.client.Details(ctx, movie.Kind(), movie.ID)
	if err != nil {
		return api.Detail{}, subMeta{}, err
	}
	return detail, subMeta{title: detail.Title, imdb: imdbNumeric(detail.IMDBID), year: detail.Year()}, nil
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

		playButton := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), func() {
			u.playLink(link.URL, link.Describe())
		})
		playButton.Importance = widget.HighImportance
		copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
			u.window.Clipboard().SetContent(link.URL)
			u.setStatus("Copied: " + link.Describe())
		})

		u.linkBox.Add(container.NewVBox(
			container.NewBorder(nil, nil, nil, container.NewHBox(playButton, copyButton), label),
			field,
			widget.NewSeparator(),
		))
	}
	u.linkBox.Refresh()
}

// renderDetail builds the plain-text details shown in the selectable box.
func renderDetail(d api.Detail) string {
	var b strings.Builder
	b.WriteString(d.Title + "\n\n")
	if d.Writer != "" {
		b.WriteString(d.Writer + "\n\n")
	}

	writeField(&b, "Released", d.Release)
	writeField(&b, "Runtime", d.Runtime)
	writeField(&b, "IMDb rating", strings.TrimSpace(d.IMDBRating))
	writeField(&b, "Quality", d.VideoQuality)
	writeField(&b, "Genre", joinNames(d.Genre))
	writeField(&b, "Country", joinNames(d.Country))
	writeField(&b, "Director", joinNames(d.Director))
	if cast := joinNames(d.Cast); cast != "" {
		writeField(&b, "Cast", truncate(cast, 300))
	}

	if d.Description != "" {
		b.WriteString("\n" + d.Description + "\n")
	}
	return b.String()
}

// imdbLink builds the public IMDb URL for a title id, or "" when there is none.
func imdbLink(imdbID string) string {
	id := strings.TrimSpace(imdbID)
	if id == "" {
		return ""
	}
	if !strings.HasPrefix(id, "tt") {
		id = "tt" + id
	}
	return "https://www.imdb.com/title/" + id + "/"
}

// setInfo sets the details text and remembers it, so the read-only revert in
// the entry's OnChanged knows what to restore.
func (u *UI) setInfo(text string) {
	u.infoText = text
	u.info.SetText(text)
}

func (u *UI) clearDetail(text string) {
	u.setInfo(text)
	if u.imdbLink != nil {
		u.imdbLink.Hide()
	}
	u.mu.Lock()
	u.links = nil
	u.detail = api.Detail{}
	u.subTitle, u.subIMDB, u.subYear = "", "", ""
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
	fmt.Fprintf(b, "%s: %s\n", label, value)
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

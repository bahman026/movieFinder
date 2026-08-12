// Package ui builds the desktop window.
package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
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

// Sources the user can switch between. The strings are both the identifier and
// the label shown in the header switcher.
const (
	sourceMovieFinder = "Database 1"
	sourceDelfan      = "Database 2"
)

// Sort modes for the results grid.
const (
	sortDefault  = "Sort: default"
	sortIMDBDesc = "IMDb high→low"
	sortIMDBAsc  = "IMDb low→high"
	sortYearDesc = "Year newest"
	sortYearAsc  = "Year oldest"
	sortTitleAsc = "Title A→Z"
)

var sortModes = []string{sortDefault, sortIMDBDesc, sortIMDBAsc, sortYearDesc, sortYearAsc, sortTitleAsc}

// sortMovies orders movies in place by the given mode. "Default" leaves the
// server's order untouched.
func sortMovies(movies []api.Movie, mode string) {
	switch mode {
	case sortIMDBDesc:
		sort.SliceStable(movies, func(i, j int) bool { return imdbValue(movies[i]) > imdbValue(movies[j]) })
	case sortIMDBAsc:
		sort.SliceStable(movies, func(i, j int) bool { return imdbValue(movies[i]) < imdbValue(movies[j]) })
	case sortYearDesc:
		sort.SliceStable(movies, func(i, j int) bool { return yearValue(movies[i]) > yearValue(movies[j]) })
	case sortYearAsc:
		sort.SliceStable(movies, func(i, j int) bool { return yearValue(movies[i]) < yearValue(movies[j]) })
	case sortTitleAsc:
		sort.SliceStable(movies, func(i, j int) bool {
			return strings.ToLower(movies[i].Title) < strings.ToLower(movies[j].Title)
		})
	}
}

// imdbValue parses a rating; a missing/invalid one sorts to the bottom of a
// high→low sort (and the top of low→high).
func imdbValue(m api.Movie) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(m.IMDBRating), 64)
	if err != nil {
		return -1
	}
	return v
}

func yearValue(m api.Movie) int {
	v, _ := strconv.Atoi(m.Year())
	return v
}

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
	sortMode string
	castMode bool
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

	grid         *widget.GridWrap
	sourceSelect *widget.Select
	search       *widget.Entry
	status      *widget.Label
	hostLabel   *widget.Label
	loadingBar  *widget.ProgressBarInfinite
	info        *widget.Entry
	infoText    string // the intended details text, so edits can be reverted
	imdbLink    *widget.Hyperlink
	detailImage *canvas.Image
	detailWant  string // the poster URL the detail pane currently expects
	linkBox     *fyne.Container
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
		sortMode:  sortDefault,
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

	// Callback attached below, after the widgets it drives exist; SetSelected
	// fires it immediately and Run() does the first load itself.
	sourceSelect := widget.NewSelect([]string{sourceMovieFinder, sourceDelfan}, nil)
	sourceSelect.SetSelected(sourceMovieFinder)
	u.sourceSelect = sourceSelect

	// Callback attached after the grid/detail pane exist (below), since
	// SetSelected fires it immediately.
	sortSelect := widget.NewSelect(sortModes, nil)
	sortSelect.SetSelected(sortDefault)

	// "Cast" toggle: when on, the search box takes an actor/director name and
	// returns their movies (Delfan source only).
	castCheck := widget.NewCheck("Cast", func(on bool) {
		u.mu.Lock()
		u.castMode = on
		alreadyDelfan := u.source == sourceDelfan
		u.mu.Unlock()
		if on {
			u.search.SetPlaceHolder("Actor / director name…")
			// Cast search only works on Delfan, so switch there automatically.
			if !alreadyDelfan {
				u.sourceSelect.SetSelected(sourceDelfan)
			}
		} else {
			u.search.SetPlaceHolder("Search movies and series, or leave empty to browse")
		}
	})

	// Search box with its cast toggle + search/clear buttons, and the source
	// picker + sort + settings on the outer edges — one clean top row.
	searchBox := container.NewBorder(nil, nil, castCheck,
		container.NewHBox(searchButton, clearButton), u.search)
	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(sourceSelect, sortSelect, refreshButton),
		settingsButton,
		searchBox,
	)

	// A thin indeterminate bar shown only while a listing is loading.
	u.loadingBar = widget.NewProgressBarInfinite()
	u.loadingBar.Hide()
	top := container.NewVBox(container.NewPadded(toolbar), u.loadingBar, widget.NewSeparator())

	u.grid = u.buildGrid()
	split := container.NewHSplit(u.grid, u.buildDetailPane())
	split.Offset = 0.63

	// Now that the grid and detail pane exist, wiring the pickers is safe.
	sourceSelect.OnChanged = func(name string) {
		u.mu.Lock()
		u.source = name
		u.query = ""
		u.mu.Unlock()
		u.search.SetText("")
		u.reload(1)
	}
	sortSelect.OnChanged = func(mode string) {
		u.mu.Lock()
		u.sortMode = mode
		sortMovies(u.movies, mode)
		u.mu.Unlock()
		u.grid.UnselectAll()
		u.grid.ScrollToTop()
		u.grid.Refresh()
		u.clearDetail("Select a title to see its details.")
	}

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

	// Single-row status bar: status text on the left, server + download
	// controls + pager on the right. One line keeps it compact.
	statusBar := container.NewBorder(nil, nil,
		u.status,
		container.NewHBox(u.hostLabel, u.dlControls, pager),
		nil,
	)
	bottom := container.NewVBox(widget.NewSeparator(), statusBar)

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

	// Poster shown alongside the details, beside the action buttons.
	u.detailImage = canvas.NewImageFromResource(theme.BrokenImageIcon())
	u.detailImage.FillMode = canvas.ImageFillContain
	u.detailImage.SetMinSize(fyne.NewSize(120, 180))

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

	// The poster on the left; the actions and IMDb link stacked to its right.
	actions := container.NewVBox(
		widget.NewLabelWithStyle("Details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(copyButton, subtitlesButton),
		u.imdbLink,
	)
	header := container.NewHBox(u.detailImage, actions)

	// A split gives the selectable details and the links their own scrollable
	// areas, so a long description does not push the links off-screen.
	body := container.NewVSplit(u.info, container.NewVScroll(u.linkBox))
	body.Offset = 0.5

	return container.NewBorder(container.NewVBox(container.NewPadded(header), widget.NewSeparator()), nil, nil, nil, body)
}

// setDetailPoster loads the first non-empty URL into the detail poster,
// discarding a late result if the selection has since changed.
func (u *UI) setDetailPoster(urls ...string) {
	u.detailImage.Resource = theme.BrokenImageIcon()
	u.detailImage.Refresh()
	for _, url := range urls {
		if strings.TrimSpace(url) == "" {
			continue
		}
		u.detailWant = url
		u.loadPoster(url, func(res fyne.Resource) {
			if u.detailWant == url {
				u.detailImage.Resource = res
				u.detailImage.Refresh()
			}
		})
		return
	}
	u.detailWant = ""
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
	castMode := u.castMode
	u.mu.RUnlock()

	searching := query != ""
	castSearch := castMode && searching

	// Cast search only works on Delfan; guide the user instead of failing.
	if castSearch && source != sourceDelfan {
		u.mu.Lock()
		u.movies = nil
		u.selected = -1
		u.mu.Unlock()
		u.grid.UnselectAll()
		u.grid.Refresh()
		u.clearDetail("Select a title to see its details.")
		u.setStatus("Cast search works on the " + sourceDelfan + " source — switch source to " + sourceDelfan + ".")
		return
	}

	// Which combinations page: MovieFinder browse and Delfan (title) search are
	// paged; MovieFinder search, Delfan browse, and cast search are not.
	paged := !castSearch && ((source == sourceMovieFinder && !searching) || (source == sourceDelfan && searching))
	if !paged {
		page = 1
	}
	switch {
	case castSearch:
		u.setStatus("Finding movies for " + query + "…")
	case searching:
		u.setStatus("Searching for " + query + "…")
	default:
		u.setStatus("Loading…")
	}
	u.loadingBar.Show()

	go func() {
		movies, host, err := u.fetch(ctx, source, query, searching, castSearch, page)
		if ctx.Err() != nil {
			return // superseded by a newer request; that request owns the bar
		}

		fyne.Do(func() {
			u.loadingBar.Hide()
			u.hostLabel.SetText("Server: " + host)
			if err != nil {
				u.setStatus("Failed: " + firstLine(err.Error()))
				dialog.ShowError(err, u.window)
				return
			}

			u.mu.Lock()
			sortMovies(movies, u.sortMode)
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
func (u *UI) fetch(ctx context.Context, source, query string, searching, castSearch bool, page int) ([]api.Movie, string, error) {
	// Cast search (Delfan only): resolve the person, then list their movies.
	if castSearch {
		casts, err := u.delfan.SearchCast(ctx, query)
		if err != nil {
			return nil, sourceDelfan, err
		}
		if len(casts) == 0 {
			return nil, sourceDelfan + " · no match", nil
		}
		person := casts[0]
		items, _, err := u.delfan.CastMovies(ctx, person.ID, 1)
		return delfanItemsToMovies(items), sourceDelfan + " · " + person.Name, err
	}

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
		return delfanItemsToMovies(items), sourceDelfan, err
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
	u.setDetailPoster(movie.PosterURL, movie.ThumbnailURL)

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
			u.showDetailContent(detail)
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

// showDetailContent fills the links area — a season/episode browser for a
// series, or a flat list of download links for a film.
func (u *UI) showDetailContent(d api.Detail) {
	if d.IsSeries() {
		u.showSeasons(d.Seasons)
		return
	}
	u.showLinks(d.DownloadLinks)
}

// showLinks lists every link with a Play and a Copy button.
func (u *UI) showLinks(links []api.DownloadLink) {
	u.linkBox.RemoveAll()

	if len(links) == 0 {
		u.linkBox.Add(widget.NewLabel("No links for this title."))
		u.linkBox.Refresh()
		return
	}

	u.linkBox.Add(widget.NewLabelWithStyle(
		fmt.Sprintf("Links (%d)", len(links)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	for _, link := range links {
		u.linkBox.Add(u.linkRow(link))
	}
	u.linkBox.Refresh()
}

// showSeasons renders a season picker and the chosen season's episodes.
func (u *UI) showSeasons(seasons []api.Season) {
	u.linkBox.RemoveAll()

	names := make([]string, len(seasons))
	for i, s := range seasons {
		names[i] = fmt.Sprintf("%s  (%d episodes)", s.Name, len(s.Episodes))
	}

	episodes := container.NewVBox()
	render := func(idx int) {
		episodes.RemoveAll()
		if idx >= 0 && idx < len(seasons) {
			for _, ep := range seasons[idx].Episodes {
				episodes.Add(u.linkRow(ep))
			}
		}
		episodes.Refresh()
	}

	seasonSelect := widget.NewSelect(names, nil)
	seasonSelect.OnChanged = func(string) { render(seasonSelect.SelectedIndex()) }

	u.linkBox.Add(widget.NewLabelWithStyle(
		fmt.Sprintf("Series — %d season/version(s)", len(seasons)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	u.linkBox.Add(container.NewBorder(nil, nil, widget.NewLabel("Season:"), nil, seasonSelect))
	u.linkBox.Add(widget.NewSeparator())
	u.linkBox.Add(episodes)

	seasonSelect.SetSelectedIndex(0) // triggers render of the first season
	u.linkBox.Refresh()
}

// linkRow builds one Play/Copy row for a download link or an episode.
func (u *UI) linkRow(link api.DownloadLink) fyne.CanvasObject {
	label := widget.NewLabel(link.Describe())
	label.TextStyle = fyne.TextStyle{Bold: true}

	// A disabled entry rather than a label, so the URL stays selectable for
	// copying by hand while being uneditable.
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

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(playButton, copyButton), label),
		field,
		widget.NewSeparator(),
	)
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
	if u.detailImage != nil {
		u.detailWant = ""
		u.detailImage.Resource = theme.BrokenImageIcon()
		u.detailImage.Refresh()
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

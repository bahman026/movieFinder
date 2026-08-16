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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/api"
	"moviefinder/internal/config"
	"moviefinder/internal/delfan"
	"moviefinder/internal/download"
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

	// The window shows one of two pages at a time: the poster grid, or a single
	// title. Browsing gets the full width for posters, and a title gets the
	// full width for its description and links, instead of both being squeezed
	// into one half of a split.
	browsePage *fyne.Container
	detailPage *fyne.Container
	pagerBox   *fyne.Container // hidden on the detail page, where it means nothing

	grid         *widget.GridWrap
	sourceSelect *widget.Select
	search       *widget.Entry
	status       *widget.Label
	hostLabel    *widget.Label
	loadingBar   *widget.ProgressBarInfinite
	info         *widget.Entry
	infoText     string // the intended details text, so edits can be reverted
	imdbLink     *widget.Hyperlink
	// Detail header: the title block and the metadata chips beside the poster.
	detailTitle    *widget.Label
	detailSubtitle *widget.Label
	detailChips    *fyne.Container
	detailImage    *canvas.Image
	detailWant     string // the poster URL the detail pane currently expects
	linkBox        *fyne.Container
	linksHeader    *widget.Label
	pageLabel      *widget.Label
	prevButton     *widget.Button
	nextButton     *widget.Button
	playDialog     dialog.Dialog
	dlControls     *fyne.Container
	pauseButton    *widget.Button
	cancelBtn      *widget.Button

	// downloads is the sequential save-to-disk queue behind the per-link
	// Download buttons. It runs one transfer at a time and yields to playback,
	// so the app never holds more than one upstream connection.
	downloads       *download.Queue
	downloadsButton *widget.Button
	downloadsBox    *fyne.Container // rows, populated only while the dialog is open
	downloadsDialog dialog.Dialog

	// activeStream is the current download-while-playing server, if any. A new
	// playback stops the previous one.
	activeStream *stream.Server
}

// delfanOptions maps the saved settings onto the Database 2 client's endpoint
// shape. It lives here rather than on config.Config so that config stays the
// bottom layer and does not have to import a client package.
func delfanOptions(cfg config.Config) delfan.Options {
	return delfan.Options{
		LoginHost:     cfg.DelfanLoginHost,
		APIHost:       cfg.DelfanAPIHost,
		BasePath:      cfg.DelfanBasePath,
		LoginEndpoint: cfg.DelfanLoginEndpoint,
		APIEndpoint:   cfg.DelfanAPIEndpoint,
		APIKey:        cfg.DelfanAPIKey,
		AppVersion:    cfg.DelfanAppVersion,
	}
}

// Run builds the window and blocks until it is closed.
func Run() {
	cfg, err := config.Load()

	a := fyneapp.NewWithID(appID)
	a.Settings().SetTheme(movieTheme{})
	a.SetIcon(appIcon)
	w := a.NewWindow("MovieFinder")
	w.SetIcon(appIcon)
	w.Resize(fyne.NewSize(1180, 760))

	u := &UI{
		app:       a,
		window:    w,
		cfg:       cfg,
		client:    api.New(cfg),
		delfan:    delfan.NewWithOptions(delfanOptions(cfg)),
		subtitles: opensubtitles.New(opensubtitles.ResolveKey(cfg.OpenSubtitlesAPIKey)),
		images:    newImageCache(),
		source:    sourceMovieFinder,
		sortMode:  sortDefault,
		page:      1,
		selected:  -1,
	}
	// Built before the content, since the widgets reference it. onChange runs on
	// the queue's worker goroutine, so it hops to the UI thread before touching
	// any widget.
	u.downloads = download.New(func() {
		fyne.Do(u.refreshDownloads)
	})

	w.SetContent(u.build())
	w.SetMaster()

	// Cancel every transfer on the way out; otherwise a queued download would
	// keep running with no window to report it.
	w.SetOnClosed(func() {
		u.downloads.StopAll()
		if u.activeStream != nil {
			u.activeStream.Stop()
		}
	})

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
	// Label carries the live count, so the queue is visible without opening it.
	u.downloadsButton = widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
		u.showDownloads()
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
		container.NewHBox(u.downloadsButton, settingsButton),
		searchBox,
	)

	// A thin indeterminate bar shown only while a listing is loading.
	u.loadingBar = widget.NewProgressBarInfinite()
	u.loadingBar.Hide()
	top := container.NewVBox(container.NewPadded(toolbar), u.loadingBar, widget.NewSeparator())

	u.grid = u.buildGrid()

	// Page one: the toolbar over a full-width grid.
	u.browsePage = container.NewBorder(top, nil, nil, nil, u.grid)

	// Page two: a back bar over the whole title. Built here so the widgets it
	// owns exist before any callback below can reach for them.
	back := widget.NewButtonWithIcon("Back to results", theme.NavigateBackIcon(), func() {
		u.showBrowse()
	})
	backBar := container.NewVBox(
		container.NewPadded(container.NewHBox(back)),
		widget.NewSeparator(),
	)
	u.detailPage = container.NewBorder(backBar, nil, nil, nil, u.buildDetailPane())
	u.detailPage.Hide()

	// Now that the grid and detail pane exist, wiring the pickers is safe.
	sourceSelect.OnChanged = func(name string) {
		u.mu.Lock()
		u.source = name
		u.mu.Unlock()
		// The search term is kept on purpose: switching source almost always
		// means "look for the same thing in the other database", and clearing
		// the box forced the user to retype it every time. reload re-runs the
		// current query against the new source.
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
	u.pagerBox = container.NewHBox(u.prevButton, u.pageLabel, u.nextButton)

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
		container.NewHBox(u.hostLabel, u.dlControls, u.pagerBox),
		nil,
	)
	bottom := container.NewVBox(widget.NewSeparator(), statusBar)

	// The status bar sits outside the page stack so that feedback from actions
	// taken on either page — "Copied", "Queued", download progress — is always
	// visible. Only the pager is page-specific, and it hides itself.
	pages := container.NewStack(u.browsePage, u.detailPage)
	return container.NewBorder(nil, bottom, nil, nil, pages)
}

// showDetail brings the single-title page forward.
func (u *UI) showDetail() {
	u.browsePage.Hide()
	u.detailPage.Show()
	u.pagerBox.Hide()
}

// showBrowse returns to the grid. The selection is dropped so that clicking the
// same poster again re-opens it — GridWrap will not re-fire OnSelected for a
// row that is already selected.
func (u *UI) showBrowse() {
	u.detailPage.Hide()
	u.browsePage.Show()
	u.pagerBox.Show()

	u.grid.UnselectAll()
	u.mu.Lock()
	u.selected = -1
	u.mu.Unlock()
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
		u.showDetail()
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
	u.detailImage.SetMinSize(fyne.NewSize(190, 285))

	// Title block: the title, the localized title under it in grey, then the
	// facts as chips — the shape the reference detail pages use, instead of the
	// "Label: value" list this pane used to lead with.
	u.detailTitle = widget.NewLabel("Select a title")
	u.detailTitle.TextStyle = fyne.TextStyle{Bold: true}
	u.detailTitle.SizeName = theme.SizeNameSubHeadingText
	u.detailTitle.Wrapping = fyne.TextWrapWord

	u.detailSubtitle = widget.NewLabel("")
	u.detailSubtitle.Importance = widget.LowImportance
	u.detailSubtitle.Wrapping = fyne.TextWrapWord
	u.detailSubtitle.Hide()

	u.detailChips = container.NewHBox()

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

	// Poster on the left, title block and actions stacked to its right. The
	// poster keeps its intrinsic size while the text column takes the rest, so
	// a long title wraps instead of squeezing the artwork.
	titleBlock := container.NewVBox(
		// Same tightening as the poster caption: the localized title belongs to
		// the line above it, not floating between it and the chips.
		container.New(layout.NewCustomPaddedVBoxLayout(-13), u.detailTitle, u.detailSubtitle),
		u.detailChips,
		container.NewHBox(copyButton, subtitlesButton, u.imdbLink),
	)
	header := container.NewBorder(nil, nil,
		container.NewVBox(u.detailImage), nil,
		container.NewPadded(titleBlock),
	)

	// Side by side rather than stacked: on a full-width page the description and
	// the links each get a readable column, and neither scrolls the other off.
	// Each keeps its own scroll area so a long synopsis cannot bury the links.
	details := container.NewBorder(
		container.NewVBox(sectionLabel("Details"), widget.NewSeparator()),
		nil, nil, nil,
		u.info,
	)
	u.linksHeader = sectionLabel("Downloads")
	links := container.NewBorder(
		container.NewVBox(u.linksHeader, widget.NewSeparator()),
		nil, nil, nil,
		container.NewVScroll(u.linkBox),
	)

	body := container.NewHSplit(details, links)
	body.Offset = 0.45

	return container.NewBorder(
		container.NewVBox(container.NewPadded(header), widget.NewSeparator()),
		nil, nil, nil,
		container.NewPadded(body),
	)
}

// setLinksHeading renames the downloads column, which is where the count of
// links (or seasons) is shown.
func (u *UI) setLinksHeading(text string) {
	if u.linksHeader != nil {
		u.linksHeader.SetText(text)
	}
}

// sectionLabel is the small heading that names a block on the detail page.
func sectionLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

// setDetailHeader fills the title block above the details text. The facts that
// become chips here are left out of renderDetail, so nothing is stated twice.
func (u *UI) setDetailHeader(d api.Detail) {
	title := strings.TrimSpace(d.Title)
	if title == "" {
		title = "Untitled"
	}
	u.detailTitle.SetText(title)

	if sub := strings.TrimSpace(d.Writer); sub != "" && sub != title {
		u.detailSubtitle.SetText(sub)
		u.detailSubtitle.Show()
	} else {
		u.detailSubtitle.SetText("")
		u.detailSubtitle.Hide()
	}

	u.detailChips.RemoveAll()
	// The rating leads in the accent colour, the way these sites badge IMDb.
	if r := strings.TrimSpace(d.IMDBRating); r != "" && r != "0" {
		u.detailChips.Add(accentChip("IMDb " + r))
	}
	for _, text := range []string{d.Year(), strings.TrimSpace(d.Runtime), strings.TrimSpace(d.VideoQuality)} {
		if text != "" {
			u.detailChips.Add(chip(text))
		}
	}
	u.detailChips.Refresh()
}

// clearDetailHeader resets the title block to its empty state.
func (u *UI) clearDetailHeader(message string) {
	u.detailTitle.SetText(message)
	u.detailSubtitle.SetText("")
	u.detailSubtitle.Hide()
	u.detailChips.RemoveAll()
	u.detailChips.Refresh()
}

// chip is a small rounded pill for one fact — year, runtime, quality.
func chip(text string) fyne.CanvasObject {
	rect := canvas.NewRectangle(colorSurface)
	rect.CornerRadius = 8
	rect.StrokeColor = colorLine
	rect.StrokeWidth = 1

	label := canvas.NewText(text, colorMuted)
	label.TextSize = 12
	return container.NewStack(rect, container.NewPadded(label))
}

// accentChip is the same pill in the accent colour, for the one fact that
// should catch the eye first.
func accentChip(text string) fyne.CanvasObject {
	rect := canvas.NewRectangle(colorAccent)
	rect.CornerRadius = 8

	label := canvas.NewText(text, colorOnAccent)
	label.TextSize = 12
	label.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewStack(rect, container.NewPadded(label))
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

			u.setDetailHeader(detail)
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
		u.setLinksHeading("Downloads")
		u.linkBox.Add(widget.NewLabel("No links for this title."))
		u.linkBox.Refresh()
		return
	}

	// The count goes in the column's own heading rather than on a second line
	// inside it, so the page does not label this list twice.
	u.setLinksHeading(fmt.Sprintf("Downloads (%d)", len(links)))
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

	u.setLinksHeading(fmt.Sprintf("Downloads — %d season/version(s)", len(seasons)))
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

	// An entry rather than a label, so the URL stays selectable for copying by
	// hand and a long one scrolls inside the row instead of widening it.
	//
	// Kept enabled — not Disable()d — for the same reason as u.info in
	// buildDetailPane: the disabled colour is ~1.4:1 against the input
	// background in the dark theme and ~1.15:1 in the light one, which renders
	// the URL invisible. Edits are reverted, so it still behaves as read-only.
	url := link.URL
	field := widget.NewEntry()
	field.SetText(url)
	field.Wrapping = fyne.TextWrapOff
	field.OnChanged = func(s string) {
		if s != url {
			field.SetText(url) // read-only: undo any typing
		}
	}

	playButton := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), func() {
		u.playLink(link.URL, link.Describe())
	})
	playButton.Importance = widget.HighImportance
	// Save without playing. Several of these stack up in the queue and run one
	// after another, which is how a whole season gets downloaded.
	downloadButton := widget.NewButtonWithIcon("Download", theme.DownloadIcon(), func() {
		u.queueDownload(link)
	})
	copyButton := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		u.window.Clipboard().SetContent(link.URL)
		u.setStatus("Copied: " + link.Describe())
	})

	return container.NewVBox(
		container.NewBorder(nil, nil, nil,
			container.NewHBox(playButton, downloadButton, copyButton), label),
		field,
		widget.NewSeparator(),
	)
}

// renderDetail builds the plain-text details shown in the selectable box.
//
// The title, localized title, rating, year, runtime and quality are deliberately
// absent: setDetailHeader shows them above as a heading and chips, and repeating
// them here would make the reader's eye do the same work twice. What is left is
// what does not fit in a chip.
func renderDetail(d api.Detail) string {
	var b strings.Builder

	writeField(&b, "Genre", joinNames(d.Genre))
	writeField(&b, "Country", joinNames(d.Country))
	writeField(&b, "Director", joinNames(d.Director))
	if cast := joinNames(d.Cast); cast != "" {
		writeField(&b, "Cast", truncate(cast, 300))
	}

	if d.Description != "" {
		b.WriteString("\n" + d.Description + "\n")
	}
	return strings.TrimLeft(b.String(), "\n")
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
	u.setInfo("")
	if u.detailTitle != nil {
		u.clearDetailHeader(text)
	}
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

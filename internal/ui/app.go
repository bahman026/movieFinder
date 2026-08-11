// Package ui builds the desktop window.
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/adlas/moviefinder/internal/api"
	"github.com/adlas/moviefinder/internal/config"
)

const appID = "at.adlas.moviefinder"

// UI owns the window and the state shown in it.
type UI struct {
	app    fyne.App
	window fyne.Window

	cfg    config.Config
	client *api.Client

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

	table      *widget.Table
	search     *widget.Entry
	status     *widget.Label
	hostLabel  *widget.Label
	progress   *widget.ProgressBar
	info       *widget.RichText
	linkSelect *widget.Select
	pageLabel  *widget.Label
	prevButton *widget.Button
	nextButton *widget.Button
}

// Run builds the window and blocks until it is closed.
func Run() {
	cfg, err := config.Load()

	a := fyneapp.NewWithID(appID)
	w := a.NewWindow("MovieFinder")
	w.Resize(fyne.NewSize(1100, 680))

	u := &UI{app: a, window: w, cfg: cfg, client: api.New(cfg), page: 1, selected: -1}
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

	u.table = u.buildTable()
	split := container.NewHSplit(u.table, u.buildDetailPane())
	split.Offset = 0.62

	u.status = widget.NewLabel("Loading…")
	u.hostLabel = widget.NewLabel("")
	u.progress = widget.NewProgressBar()
	u.progress.Hide()

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
		container.NewVBox(u.status, u.hostLabel, u.progress),
	)

	return container.NewBorder(toolbar, statusBar, nil, nil, split)
}

func (u *UI) buildDetailPane() fyne.CanvasObject {
	u.info = widget.NewRichTextFromMarkdown("_Select a title to see its details._")
	u.info.Wrapping = fyne.TextWrapWord

	u.linkSelect = widget.NewSelect(nil, nil)
	u.linkSelect.PlaceHolder = "No download links"
	u.linkSelect.Disable()

	downloadButton := widget.NewButtonWithIcon("Download", theme.DownloadIcon(), func() {
		u.downloadSelectedLink()
	})
	copyButton := widget.NewButtonWithIcon("Copy link", theme.ContentCopyIcon(), func() {
		u.copySelectedLink()
	})

	return container.NewBorder(
		widget.NewLabelWithStyle("Details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewVBox(u.linkSelect, container.NewHBox(downloadButton, copyButton)),
		nil, nil,
		container.NewVScroll(u.info),
	)
}

var columns = []struct {
	title string
	width float32
	value func(api.Movie) string
}{
	{"Title", 300, func(m api.Movie) string { return m.Title }},
	{"Year", 60, func(m api.Movie) string { return m.Year() }},
	{"IMDb", 60, func(m api.Movie) string { return m.IMDBRating }},
	{"Runtime", 90, func(m api.Movie) string { return m.Runtime }},
	{"Kind", 70, func(m api.Movie) string { return m.KindLabel() }},
	{"Quality", 130, func(m api.Movie) string { return m.VideoQuality }},
}

func (u *UI) buildTable() *widget.Table {
	table := widget.NewTable(
		func() (int, int) {
			u.mu.RLock()
			defer u.mu.RUnlock()
			return len(u.movies), len(columns)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label, ok := cell.(*widget.Label)
			if !ok {
				return
			}
			movie, ok := u.movieAt(id.Row)
			if !ok || id.Col >= len(columns) {
				label.SetText("")
				return
			}
			label.SetText(columns[id.Col].value(movie))
		},
	)

	table.ShowHeaderRow = true
	table.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	table.UpdateHeader = func(id widget.TableCellID, cell fyne.CanvasObject) {
		if label, ok := cell.(*widget.Label); ok && id.Col >= 0 && id.Col < len(columns) {
			label.SetText(columns[id.Col].title)
		}
	}
	for i, col := range columns {
		table.SetColumnWidth(i, col.width)
	}
	table.OnSelected = func(id widget.TableCellID) {
		u.mu.Lock()
		u.selected = id.Row
		u.mu.Unlock()
		u.loadDetail(id.Row)
	}
	return table
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
		u.setStatus("Loading page " + fmt.Sprint(page) + "…")
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

			u.table.UnselectAll()
			u.table.ScrollToTop()
			u.table.Refresh()
			u.clearDetail("_Select a title to see its details._")

			u.pageLabel.SetText(fmt.Sprintf("Page %d", page))
			// The search endpoint returns everything at once, so paging is
			// meaningless there.
			switch {
			case searching:
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

// loadDetail fetches the full record for a row and fills the detail pane.
func (u *UI) loadDetail(row int) {
	movie, ok := u.movieAt(row)
	if !ok {
		return
	}

	if u.cancelDetail != nil {
		u.cancelDetail()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancelDetail = cancel

	u.clearDetail("## " + movie.Title + "\n\n_Loading details…_")

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

			options := make([]string, 0, len(detail.DownloadLinks))
			for _, link := range detail.DownloadLinks {
				options = append(options, link.Describe())
			}
			u.linkSelect.Options = options
			if len(options) == 0 {
				u.linkSelect.PlaceHolder = "No download links"
				u.linkSelect.ClearSelected()
				u.linkSelect.Disable()
			} else {
				u.linkSelect.PlaceHolder = "Choose a file"
				u.linkSelect.Enable()
				u.linkSelect.SetSelectedIndex(0)
			}
			u.linkSelect.Refresh()
		})
	}()
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

	if len(d.DownloadLinks) > 0 {
		fmt.Fprintf(&b, "\n### Downloads (%d)\n\n", len(d.DownloadLinks))
		for _, link := range d.DownloadLinks {
			fmt.Fprintf(&b, "- %s\n", link.Describe())
		}
	} else if !d.DownloadsEnabled() {
		b.WriteString("\n_Downloads are disabled for this title._\n")
	}

	if d.Description != "" {
		fmt.Fprintf(&b, "\n### Description\n\n%s\n", d.Description)
	}
	return b.String()
}

func (u *UI) downloadSelectedLink() {
	link, ok := u.selectedLink()
	if !ok {
		u.setStatus("Choose a file to download first.")
		return
	}

	u.mu.RLock()
	title := u.detail.Title
	u.mu.RUnlock()

	target := filepath.Join(u.cfg.ResolveDownloadDir(), downloadFileName(title, link))
	out, err := os.Create(target)
	if err != nil {
		dialog.ShowError(err, u.window)
		return
	}

	u.progress.SetValue(0)
	u.progress.Show()
	u.setStatus("Downloading " + filepath.Base(target) + "…")

	go func() {
		defer out.Close()

		err := u.client.Download(context.Background(), link, out, func(written, total int64) {
			if total <= 0 {
				return
			}
			fyne.Do(func() { u.progress.SetValue(float64(written) / float64(total)) })
		})

		fyne.Do(func() {
			u.progress.Hide()
			if err != nil {
				os.Remove(target)
				u.setStatus("Download failed: " + firstLine(err.Error()))
				dialog.ShowError(err, u.window)
				return
			}
			u.setStatus("Saved to " + target)
		})
	}()
}

func (u *UI) copySelectedLink() {
	link, ok := u.selectedLink()
	if !ok {
		u.setStatus("Choose a file first.")
		return
	}
	u.window.Clipboard().SetContent(link.URL)
	u.setStatus("Link copied.")
}

func (u *UI) clearDetail(markdown string) {
	u.info.ParseMarkdown(markdown)
	u.mu.Lock()
	u.links = nil
	u.detail = api.Detail{}
	u.mu.Unlock()
	u.linkSelect.Options = nil
	u.linkSelect.PlaceHolder = "No download links"
	u.linkSelect.ClearSelected()
	u.linkSelect.Disable()
	u.linkSelect.Refresh()
}

func (u *UI) movieAt(row int) (api.Movie, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if row < 0 || row >= len(u.movies) {
		return api.Movie{}, false
	}
	return u.movies[row], true
}

// selectedLink resolves the link picker's choice back to a DownloadLink.
func (u *UI) selectedLink() (api.DownloadLink, bool) {
	index := u.linkSelect.SelectedIndex()
	u.mu.RLock()
	defer u.mu.RUnlock()
	if index < 0 || index >= len(u.links) {
		return api.DownloadLink{}, false
	}
	return u.links[index], true
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

// downloadFileName builds a save name from the title and the chosen link,
// keeping the extension the URL implies.
func downloadFileName(title string, link api.DownloadLink) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = "download"
	}
	if label := strings.TrimSpace(link.Label); label != "" {
		name += " " + label
	}

	// Sanitise and shorten the stem before reattaching the extension, so a
	// long title cannot truncate the extension away.
	return safeFileName(name) + unsafeName.ReplaceAllString(link.Ext(), "_")
}

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// safeFileName strips the characters Windows rejects in a path so an
// API-supplied name cannot escape the download folder.
func safeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = unsafeName.ReplaceAllString(name, "_")
	name = strings.Trim(name, ". ")
	if name == "" {
		return "download.bin"
	}
	if len(name) > 150 {
		name = name[:150]
	}
	return name
}

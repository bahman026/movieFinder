package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// subtitleLanguages are the choices offered in the language picker — enough to
// cover common cases without listing every one of the ~100 OpenSubtitles
// supports. English is first and is the default, matching a typical VLC
// subtitle search.
var subtitleLanguages = []struct{ code, label string }{
	{"en", "English"},
	{"fa", "Persian (Farsi)"},
	{"ar", "Arabic"},
	{"fr", "French"},
	{"de", "German"},
	{"es", "Spanish"},
	{"it", "Italian"},
	{"tr", "Turkish"},
	{"ru", "Russian"},
	{"pt", "Portuguese"},
}

func languageLabels() []string {
	labels := make([]string, len(subtitleLanguages))
	for i, l := range subtitleLanguages {
		labels[i] = l.label
	}
	return labels
}

func languageLabel(code string) string {
	for _, l := range subtitleLanguages {
		if l.code == code {
			return l.label
		}
	}
	return code
}

func languageCode(label string) string {
	for _, l := range subtitleLanguages {
		if l.label == label {
			return l.code
		}
	}
	return "en"
}

// imdbNumeric strips the "tt" prefix and leading zeros the API's imdb_id
// field carries but OpenSubtitles' imdb_id parameter does not expect.
func imdbNumeric(id string) string {
	id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt")
	id = strings.TrimLeft(id, "0")
	return id
}

// showSubtitles opens a dialog to search and download subtitles for the
// currently loaded detail, defaulting to English and the configured source.
func (u *UI) showSubtitles() {
	u.mu.RLock()
	title := u.subTitle
	year := u.subYear
	imdbID := u.subIMDB
	u.mu.RUnlock()

	if strings.TrimSpace(title) == "" {
		dialog.ShowInformation("Subtitles", "Select a title first.", u.window)
		return
	}

	status := widget.NewLabel("Searching…")
	results := container.NewVBox()

	var controls *subtitleControls
	var cancelSearch context.CancelFunc
	runSearch := func() {
		if cancelSearch != nil {
			cancelSearch()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cancelSearch = cancel

		search := u.subtitleSearcher(controls.sourceCode())
		query := controls.query(title, imdbID, year)
		language := controls.language.Selected
		sourceName := controls.source.Selected
		status.SetText("Searching…")
		results.RemoveAll()
		results.Refresh()

		u.bg("The subtitle search", func() {
			subs, err := search(ctx, query)
			if ctx.Err() != nil {
				return // superseded by a newer search (language changed again)
			}

			u.onUI("The subtitle search", func() {
				if err != nil {
					status.SetText(sourceFailureMessage(sourceName, err))
					return
				}
				if len(subs) == 0 {
					status.SetText(fmt.Sprintf("No %s subtitles found for %s.", language, title))
					return
				}
				status.SetText(fmt.Sprintf("%d result(s).", len(subs)))
				for _, sub := range subs {
					results.Add(u.subtitleRow(sub))
				}
				results.Refresh()
			})
		})
	}
	controls = u.newSubtitleControls(runSearch)

	heading := title
	if year != "" {
		heading += " (" + year + ")"
	}

	content := container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle(heading, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			controls.widget,
			status,
			widget.NewSeparator(),
		),
		nil, nil, nil,
		container.NewVScroll(results),
	)

	d := dialog.NewCustom("Subtitles", "Close", content, u.window)
	d.Resize(fyne.NewSize(640, 520))
	d.Show()

	runSearch()
}

// subtitleRow renders one search result with a Download button.
func (u *UI) subtitleRow(sub subtitleHit) fyne.CanvasObject {
	titleLabel := widget.NewLabelWithStyle(sub.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	release := widget.NewLabel(sub.release)
	release.Wrapping = fyne.TextWrapWord

	metaLabel := widget.NewLabel(strings.Join(sub.meta, " · "))
	metaLabel.TextStyle = fyne.TextStyle{Italic: true}

	downloadButton := widget.NewButtonWithIcon("Download", theme.DownloadIcon(), nil)
	downloadButton.OnTapped = func() { u.downloadSubtitle(sub, downloadButton) }

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, downloadButton, titleLabel),
		release,
		metaLabel,
		widget.NewSeparator(),
	)
}

func (u *UI) downloadSubtitle(sub subtitleHit, button *widget.Button) {
	button.Disable()
	button.SetText("Downloading…")

	u.bg("The subtitle download", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		data, fileName, err := sub.download(ctx)

		u.onUI("The subtitle download", func() {
			button.Enable()
			button.SetText("Download")

			if err != nil {
				dialog.ShowError(err, u.window)
				return
			}
			if fileName == "" {
				fileName = sub.release
			}
			u.saveSubtitle(safeSubtitleName(fileName), data)
		})
	})
}

// saveSubtitle lets the user pick where to keep the file via the OS's native
// save dialog, rather than a configured folder — the video it belongs with
// could be anywhere.
func (u *UI) saveSubtitle(suggestedName string, data []byte) {
	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		if writer == nil {
			return // user cancelled
		}
		defer writer.Close()

		if _, err := writer.Write(data); err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		u.setStatus("Saved " + writer.URI().Name())
	}, u.window)

	saveDialog.SetFileName(suggestedName)
	saveDialog.Show()
}

var unsafeSubtitleChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// safeSubtitleName strips characters Windows rejects and guarantees a
// .srt-ish extension, so a save dialog pre-fill never looks broken.
func safeSubtitleName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = unsafeSubtitleChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, ". ")
	if name == "" {
		return "subtitle.srt"
	}
	if len(name) > 150 {
		name = name[:150]
	}
	if filepath.Ext(name) == "" {
		name += ".srt"
	}
	return name
}

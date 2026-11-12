package ui

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/api"
	"moviefinder/internal/download"
)

// queueDownload asks where to save a link and puts it at the back of the queue.
// Unlike Play, nothing launches and nothing waits: the transfer starts when the
// queue reaches it.
func (u *UI) queueDownload(link api.DownloadLink) {
	save := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		if writer == nil {
			return // cancelled
		}
		savePath := uriToPath(writer.URI())
		writer.Close() // the queue's stream server opens the file itself

		id := u.downloads.Add(link.Describe(), link.URL, savePath)

		// Captured now: by the time this finishes, the detail pane may show a
		// different title entirely.
		u.mu.RLock()
		meta := autoSubJob{title: u.subTitle, imdb: u.subIMDB, year: u.subYear, series: u.detail.IsSeries()}
		u.mu.RUnlock()
		if meta.series {
			meta.season, meta.episode = episodeFrom(filepath.Base(savePath), link.Label, suggestedSaveName(link.URL))
		}
		u.rememberAutoSub(id, meta)

		active, queued, _ := u.downloads.Counts()
		if active+queued > 1 {
			u.setStatus(fmt.Sprintf("Queued: %s (%d waiting)", link.Describe(), queued))
		} else {
			u.setStatus("Downloading: " + link.Describe())
		}
	}, u.window)

	save.SetFileName(suggestedSaveName(link.URL))
	save.Show()
}

// showDownloads opens the queue. The rows are rebuilt by refreshDownloads while
// this is on screen, which is why downloadsBox is only populated here.
func (u *UI) showDownloads() {
	u.downloadsBox = container.NewVBox()

	clearButton := widget.NewButtonWithIcon("Clear finished", theme.DeleteIcon(), func() {
		u.downloads.ClearFinished()
	})

	header := container.NewVBox(
		widget.NewLabelWithStyle("Downloads", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("One file downloads at a time. Pause the running one and the next "+
			"in line starts immediately; resuming continues from where it stopped, and the "+
			"arrows change what goes next. Playback takes priority: a download pauses while "+
			"you stream and resumes when you stop."),
		container.NewHBox(clearButton),
		widget.NewSeparator(),
	)

	content := container.NewBorder(header, nil, nil, nil,
		container.NewVScroll(u.downloadsBox))

	d := dialog.NewCustom("Downloads", "Close", content, u.window)
	d.Resize(fyne.NewSize(720, 520))
	d.SetOnClosed(func() {
		// Stop rebuilding rows nobody is looking at.
		u.downloadsBox = nil
		u.downloadsDialog = nil
	})
	u.downloadsDialog = d

	u.refreshDownloads()
	d.Show()
}

// refreshDownloads updates the toolbar count and, when the queue dialog is
// open, its rows. Called from the queue's onChange (already marshalled onto the
// UI thread) and after any local change.
func (u *UI) refreshDownloads() {
	if u.downloads == nil {
		return
	}

	active, queued, done := u.downloads.Counts()
	if u.downloadsButton != nil {
		switch {
		case active+queued > 0:
			u.downloadsButton.SetText(fmt.Sprintf("%d", active+queued))
		case done > 0:
			u.downloadsButton.SetText("")
		default:
			u.downloadsButton.SetText("")
		}
	}

	jobs := u.downloads.Jobs()
	u.updateQueueProgress(jobs, queued)

	if u.downloadsBox == nil {
		return // dialog closed; nothing to draw
	}

	u.downloadsBox.RemoveAll()
	if len(jobs) == 0 {
		u.downloadsBox.Add(widget.NewLabel("No downloads yet. Use the Download button on any link."))
		u.downloadsBox.Refresh()
		return
	}
	for i, job := range jobs {
		u.downloadsBox.Add(u.downloadRow(job, i, len(jobs)))
	}
	u.downloadsBox.Refresh()
}

// downloadRow renders one queue entry: what it is, how far along, and the
// actions that apply to the state it is in. index and count place it in the
// list, which is what decides whether it can be moved up or down.
func (u *UI) downloadRow(job download.Snapshot, index, count int) fyne.CanvasObject {
	title := widget.NewLabelWithStyle(job.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	bar := widget.NewProgressBar()
	bar.Min, bar.Max = 0, 1
	if job.Total > 0 {
		bar.SetValue(float64(job.Downloaded) / float64(job.Total))
	} else if job.State == download.StateDone {
		bar.SetValue(1)
	}

	text := describeJob(job)
	if post := u.postStatusOf(job.ID); post != "" {
		text += "\n" + post
	}
	status := widget.NewLabel(text)
	status.Wrapping = fyne.TextWrapWord

	var actions []fyne.CanvasObject

	// Reordering only means something for a job that has yet to run: it picks
	// what the queue takes next.
	if !job.State.Done() {
		up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() { u.downloads.MoveUp(job.ID) })
		down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() { u.downloads.MoveDown(job.ID) })
		if index == 0 {
			up.Disable()
		}
		if index == count-1 {
			down.Disable()
		}
		actions = append(actions, up, down)
	}

	switch job.State {
	case download.StateDownloading, download.StateQueued:
		// Pausing what is waiting holds it back; pausing what is running hands
		// the connection to whatever is next.
		actions = append(actions, widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() {
			u.downloads.Pause(job.ID)
		}))
	case download.StatePaused:
		actions = append(actions, widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() {
			u.downloads.Resume(job.ID)
		}))
	}

	if job.State.Done() {
		remove := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
			u.downloads.Remove(job.ID)
		})
		actions = append(actions, remove)
	} else {
		cancel := widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
			u.downloads.Cancel(job.ID)
		})
		cancel.Importance = widget.DangerImportance
		actions = append(actions, cancel)
	}

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(actions...), title),
		bar,
		status,
		widget.NewSeparator(),
	)
}

// describeJob is the one-line explanation under each progress bar.
func describeJob(job download.Snapshot) string {
	name := filepath.Base(job.SavePath)
	switch job.State {
	case download.StateQueued:
		return "Waiting — " + name
	case download.StateDownloading:
		return fmt.Sprintf("Downloading %s — %s", progressPercent(job.Downloaded, job.Total), name)
	case download.StatePaused:
		// Worth spelling out: the bytes so far are kept, and resuming picks up
		// from them rather than starting the file again.
		return fmt.Sprintf("Paused at %s, continues from here — %s",
			progressPercent(job.Downloaded, job.Total), name)
	case download.StateDone:
		return "Saved to " + job.SavePath
	case download.StateFailed:
		msg := "unknown error"
		if job.Err != nil {
			msg = firstLine(job.Err.Error())
		}
		return "Failed: " + msg
	case download.StateCanceled:
		return "Canceled — partial file left at " + job.SavePath
	}
	return name
}

// updateQueueProgress drives the status-bar readout. Only a running transfer is
// shown: a paused or waiting job is already reported by the count on the
// toolbar button, and leaving a frozen bar on the status bar would read as a
// stalled download. queued excludes the running job, so it is the number still
// behind it.
func (u *UI) updateQueueProgress(jobs []download.Snapshot, queued int) {
	if u.queueProgress == nil {
		return // status bar not built yet
	}

	var running *download.Snapshot
	for i := range jobs {
		if jobs[i].State == download.StateDownloading {
			running = &jobs[i]
			break
		}
	}
	if running == nil {
		u.queueProgress.Hide()
		return
	}

	// Total is -1 when the host sends no length, and the percentage is then
	// unknowable — show the bytes alone rather than a bar pinned at zero.
	if running.Total > 0 {
		u.queueProgressBar.SetValue(float64(running.Downloaded) / float64(running.Total))
		u.queueProgressWrap.Show()
	} else {
		u.queueProgressWrap.Hide()
	}

	u.queueProgressLabel.SetText(queueProgressText(*running, queued))
	u.queueProgress.Show()
}

// queueProgressText is the wording beside the status-bar progress bar: the byte
// counts the bar's own percentage cannot carry, plus what is still waiting.
func queueProgressText(job download.Snapshot, queued int) string {
	var text string
	if job.Total > 0 {
		text = fmt.Sprintf("%s / %s", humanBytes(job.Downloaded), humanBytes(job.Total))
	} else {
		text = humanBytes(job.Downloaded) + " downloaded"
	}
	if queued > 0 {
		text += fmt.Sprintf("  (+%d waiting)", queued)
	}
	return text
}

package ui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/opensubtitles"
	"moviefinder/internal/player"
	"moviefinder/internal/stream"
)

// playLink opens the playback dialog for a stream: choose whether to save a
// copy while watching, optionally attach an OpenSubtitles subtitle, then launch
// an external player.
func (u *UI) playLink(videoURL, linkLabel string) {
	p, err := player.Detect(u.cfg.PlayerPath)
	if err != nil {
		dialog.ShowError(err, u.window)
		return
	}

	u.mu.RLock()
	title := u.subTitle
	year := u.subYear
	imdbID := u.subIMDB
	u.mu.RUnlock()

	downloadCheck := widget.NewCheck("Download while playing (save a copy)", nil)
	downloadCheck.SetChecked(true)

	// doPlay is the single commit point for every play action in this dialog.
	// The subtitle is passed as bytes (not a path) because where it is written
	// depends on the choice below: when saving a copy, it goes next to the movie
	// file with the same basename; otherwise it goes to a temp file.
	doPlay := func(subData []byte, subName string) {
		if downloadCheck.Checked {
			u.startDownloadAndPlay(p, videoURL, linkLabel, subData, subName)
		} else {
			u.launchDirect(p, videoURL, subData, subName)
		}
	}

	status := widget.NewLabel("Searching subtitles…")
	results := container.NewVBox()

	langSelect := widget.NewSelect(languageLabels(), nil)
	langSelect.SetSelected(languageLabel("en"))

	playNow := widget.NewButtonWithIcon("Play without subtitle", theme.MediaPlayIcon(), func() {
		doPlay(nil, "")
	})

	var cancelSearch context.CancelFunc
	runSearch := func() {
		if strings.TrimSpace(title) == "" {
			status.SetText("No title to search subtitles for — play without one.")
			return
		}
		if cancelSearch != nil {
			cancelSearch()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cancelSearch = cancel

		lang := languageCode(langSelect.Selected)
		status.SetText("Searching subtitles…")
		results.RemoveAll()
		results.Refresh()

		go func() {
			subs, err := u.subtitles.Search(ctx, title, imdbID, year, lang)
			if ctx.Err() != nil {
				return
			}
			fyne.Do(func() {
				if err != nil {
					status.SetText("Subtitle search failed: " + firstLine(err.Error()))
					return
				}
				if len(subs) == 0 {
					status.SetText(fmt.Sprintf("No %s subtitles — you can still play without one.", langSelect.Selected))
					return
				}
				status.SetText(fmt.Sprintf("%d subtitle(s) — pick one to play with, or play without.", len(subs)))
				for _, sub := range subs {
					results.Add(u.playSubtitleRow(sub, doPlay))
				}
				results.Refresh()
			})
		}()
	}
	langSelect.OnChanged = func(string) { runSearch() }

	header := container.NewVBox(
		widget.NewLabelWithStyle("Play: "+linkLabel, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Player: "+p.Name),
		downloadCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Subtitle language:"), nil, langSelect),
		playNow,
		status,
		widget.NewSeparator(),
	)

	content := container.NewBorder(header, nil, nil, nil, container.NewVScroll(results))
	d := dialog.NewCustom("Play", "Close", content, u.window)
	d.Resize(fyne.NewSize(660, 560))
	u.playDialog = d
	d.Show()

	runSearch()
}

// playSubtitleRow renders one subtitle with a "Play with this" button that
// downloads the subtitle bytes, then hands them to doPlay.
func (u *UI) playSubtitleRow(sub opensubtitles.Subtitle, doPlay func(subData []byte, subName string)) fyne.CanvasObject {
	name := sub.MovieName
	if name == "" {
		name = "(unknown title)"
	}
	if sub.Year != "" {
		name += " (" + sub.Year + ")"
	}
	titleLabel := widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	release := widget.NewLabel(sub.Release)
	release.Wrapping = fyne.TextWrapWord

	var meta []string
	if d := sub.UploadDateLabel(); d != "" {
		meta = append(meta, d)
	}
	meta = append(meta, fmt.Sprintf("%d downloads", sub.DownloadCount))
	if sub.HD {
		meta = append(meta, "HD")
	}
	metaLabel := widget.NewLabel(strings.Join(meta, " · "))
	metaLabel.TextStyle = fyne.TextStyle{Italic: true}

	playButton := widget.NewButtonWithIcon("Play with this", theme.MediaPlayIcon(), nil)
	playButton.OnTapped = func() {
		playButton.Disable()
		playButton.SetText("Preparing…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			data, fileName, err := u.subtitles.Download(ctx, sub.FileID)
			fyne.Do(func() {
				playButton.Enable()
				playButton.SetText("Play with this")
				if err != nil {
					dialog.ShowError(err, u.window)
					return
				}
				doPlay(data, fileName)
			})
		}()
	}

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, playButton, titleLabel),
		release,
		metaLabel,
		widget.NewSeparator(),
	)
}

// launchDirect streams straight from the remote URL, no local copy. Any
// subtitle goes to a temp file, since there is no saved movie to sit beside.
func (u *UI) launchDirect(p player.Player, videoURL string, subData []byte, subName string) {
	subPath := ""
	if len(subData) > 0 {
		path, err := writeTempSubtitle(subName, "", subData)
		if err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		subPath = path
	}
	if err := p.Play(videoURL, subPath); err != nil {
		dialog.ShowError(err, u.window)
		return
	}
	u.setStatus(playingStatus(p, subPath))
	u.closePlayDialog()
}

// startDownloadAndPlay asks where to save, opens ONE upstream connection that
// both fills the save file and feeds the player from localhost, then launches
// the player and reports progress. A chosen subtitle is written next to the
// movie with the same basename, so when the download finishes the movie and its
// subtitle sit side by side (Movie.mkv / Movie.srt).
func (u *UI) startDownloadAndPlay(p player.Player, remoteURL, linkLabel string, subData []byte, subName string) {
	save := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		if writer == nil {
			return // cancelled
		}
		savePath := uriToPath(writer.URI())
		writer.Close() // the stream server opens the file itself

		// Write the subtitle beside the movie, sharing its basename.
		subPath := ""
		if len(subData) > 0 {
			sp, werr := writeSubtitleBeside(savePath, subName, subData)
			if werr != nil {
				dialog.ShowError(werr, u.window)
				return
			}
			subPath = sp
		}

		// Replace any previous stream.
		if u.activeStream != nil {
			u.activeStream.Stop()
		}

		srv := stream.New(remoteURL, savePath)
		local, err := srv.Start(context.Background())
		if err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		u.activeStream = srv

		if err := p.Play(local, subPath); err != nil {
			srv.Stop()
			u.activeStream = nil
			dialog.ShowError(err, u.window)
			return
		}
		u.setStatus("Streaming + saving…")
		u.showDownloadControls()
		u.closePlayDialog()
		go u.watchProgress(srv, savePath)
	}, u.window)

	save.SetFileName(suggestedSaveName(remoteURL))
	save.Show()
}

// writeSubtitleBeside saves the subtitle in the movie's folder with the movie's
// basename (Movie.mkv -> Movie.srt), keeping the subtitle's own extension.
func writeSubtitleBeside(moviePath, subName string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(subName))
	if ext == "" || len(ext) > 5 {
		ext = ".srt"
	}
	base := strings.TrimSuffix(moviePath, filepath.Ext(moviePath))
	subPath := base + ext
	if err := os.WriteFile(subPath, data, 0o644); err != nil {
		return "", fmt.Errorf("save subtitle: %w", err)
	}
	return subPath, nil
}

// watchProgress updates the status bar as the download proceeds. It stops
// updating once this stream is superseded by a newer one.
func (u *UI) watchProgress(srv *stream.Server, savePath string) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		downloaded, total, done, derr := srv.Progress()
		stop := false
		fyne.Do(func() {
			if u.activeStream != srv {
				stop = true // a newer playback (or a cancel) took over
				return
			}
			switch {
			case derr != nil:
				u.setStatus("Download stopped: " + firstLine(derr.Error()))
				u.finishDownload(srv)
				stop = true
			case done:
				u.setStatus("Saved to " + savePath)
				u.finishDownload(srv)
				stop = true
			case srv.IsPaused():
				u.setStatus(fmt.Sprintf("Paused: %s (%s)",
					progressPercent(downloaded, total), filepath.Base(savePath)))
			default:
				u.setStatus(fmt.Sprintf("Streaming + saving: %s (%s)",
					progressPercent(downloaded, total), filepath.Base(savePath)))
			}
		})
		if stop {
			return
		}
	}
}

// togglePause flips the active download between paused and running.
func (u *UI) togglePause() {
	if u.activeStream == nil {
		return
	}
	if u.activeStream.IsPaused() {
		u.activeStream.Resume()
		u.pauseButton.SetText("Pause")
		u.pauseButton.SetIcon(theme.MediaPauseIcon())
	} else {
		u.activeStream.Pause()
		u.pauseButton.SetText("Resume")
		u.pauseButton.SetIcon(theme.MediaPlayIcon())
	}
}

// cancelDownload stops the active download and hides the controls. The partial
// file is left on disk.
func (u *UI) cancelDownload() {
	if u.activeStream == nil {
		return
	}
	u.activeStream.Stop()
	u.activeStream = nil
	u.hideDownloadControls()
	u.setStatus("Download canceled.")
}

func (u *UI) showDownloadControls() {
	u.pauseButton.SetText("Pause")
	u.pauseButton.SetIcon(theme.MediaPauseIcon())
	u.dlControls.Show()
}

func (u *UI) hideDownloadControls() { u.dlControls.Hide() }

// finishDownload clears the active-stream state when a download ends on its own.
func (u *UI) finishDownload(srv *stream.Server) {
	if u.activeStream == srv {
		u.activeStream = nil
	}
	u.hideDownloadControls()
}

func (u *UI) closePlayDialog() {
	if u.playDialog != nil {
		u.playDialog.Hide()
		u.playDialog = nil
	}
}

func playingStatus(p player.Player, subtitlePath string) string {
	if subtitlePath != "" {
		return "Playing in " + p.Name + " with subtitle."
	}
	return "Playing in " + p.Name + "."
}

// writeTempSubtitle saves a downloaded subtitle to a temp file the player reads.
func writeTempSubtitle(fileName, release string, data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "moviefinder-subs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create subtitle temp dir: %w", err)
	}
	name := fileName
	if strings.TrimSpace(name) == "" {
		name = release
	}
	target := filepath.Join(dir, safeSubtitleName(name))
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", fmt.Errorf("save subtitle: %w", err)
	}
	return target, nil
}

// suggestedSaveName derives a sensible filename from the stream URL, which
// usually already ends in the real release name (e.g. The.Eyes.2026.480p.mkv).
func suggestedSaveName(remoteURL string) string {
	base := path.Base(strings.SplitN(remoteURL, "?", 2)[0])
	base = safeSubtitleName(base) // reuse the same character stripping
	if filepath.Ext(base) == ".srt" {
		// safeSubtitleName appends .srt when there is no extension; undo that so
		// a videoless URL still gets a video extension.
		base = strings.TrimSuffix(base, ".srt") + ".mkv"
	}
	if base == "" {
		base = "movie.mkv"
	}
	return base
}

// uriToPath turns a Fyne file URI into an OS path, fixing the leading-slash the
// Windows file URI carries (/C:/... -> C:\...).
func uriToPath(u fyne.URI) string {
	p := u.Path()
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func progressPercent(downloaded, total int64) string {
	if total <= 0 {
		return humanBytes(downloaded)
	}
	return fmt.Sprintf("%d%% — %s / %s", downloaded*100/total, humanBytes(downloaded), humanBytes(total))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

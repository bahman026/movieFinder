package ui

import (
	"fyne.io/fyne/v2"

	"moviefinder/internal/safe"
)

// This file is the app's answer to "an outside service misbehaved".
//
// Failures that the client packages see coming are already returned as errors
// and shown in the status bar. What is left is the unforeseen kind: a rotated
// API answering with a different shape, a scraped page restyled overnight, an
// image host serving HTML. Those surface as panics in a parser, and a panic on
// any goroutine ends the whole process — which, mid-download, is the worst
// possible outcome.
//
// So every background task and every hop back to the UI thread goes through
// bg/onUI. The task dies, a line appears in the status bar naming it, and the
// browsing, downloading and playback that were already working keep working.

// bg runs fn off the UI thread. what names the task in the status bar if it
// falls over, so the message says which feature failed and not just "error".
func (u *UI) bg(what string, fn func()) {
	safe.Go(fn, func(err error) { u.reportFailure(what, err, true) })
}

// onUI runs fn on the UI thread, guarded. Use it in place of a bare fyne.Do:
// that callback runs on the main loop, so a panic inside it takes the window
// down with it.
func (u *UI) onUI(what string, fn func()) {
	fyne.Do(safe.Wrap(fn, func(err error) { u.reportFailure(what, err, false) }))
}

// reportFailure puts a recovered panic in the status bar. fromBackground says
// whether the caller is off the UI thread and so needs a hop onto it; calling
// fyne.Do from a callback already running on the UI thread is itself an error.
func (u *UI) reportFailure(what string, err error, fromBackground bool) {
	msg := what + " failed unexpectedly: " + firstLine(err.Error()) +
		". Everything else still works."

	if !fromBackground {
		u.setStatus(msg)
		return
	}
	// The hop is a bare fyne.Do on purpose: wrapping it in onUI would risk
	// recursing through this same reporter if setStatus were the thing that
	// panicked.
	fyne.Do(func() { u.setStatus(msg) })
}

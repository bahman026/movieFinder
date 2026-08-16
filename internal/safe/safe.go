// Package safe runs work that must not be able to take the whole app down
// with it.
//
// A panic on any goroutine kills the entire Go process, and most of this app's
// goroutines are parsing something a remote server sent: a scraped page whose
// layout changed, an API that started answering with a different shape, a
// mirror serving an error page where JSON was expected. None of that should
// close a window in the middle of a download.
//
// So every background task and every callback that runs remote data through a
// parser goes through here. A panic becomes an error handed to a Reporter —
// typically a line in the status bar — and everything else keeps running.
//
// This is a backstop, not a licence to skip error handling: an expected
// failure should still be returned as an error by the code that meets it.
package safe

import (
	"fmt"
	"log"
	"runtime/debug"
)

// Reporter is told about a recovered panic. It may be nil, in which case the
// panic is only logged.
type Reporter func(error)

// Go runs fn on a new goroutine, guarded by Run.
func Go(fn func(), report Reporter) {
	go Run(fn, report)
}

// Run calls fn on the current goroutine and reports whether it finished
// without panicking. A panic is recovered, logged with its stack, and passed
// to report as an error.
func Run(fn func(), report Reporter) (ok bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		ok = false

		err := asError(r)
		// The stack is the only thing that says where this came from, but it is
		// far too long for a status bar, so it goes to the log and the short
		// form goes to the reporter.
		log.Printf("recovered panic: %v\n%s", err, debug.Stack())
		if report != nil {
			report(err)
		}
	}()

	fn()
	return true
}

// Wrap returns fn guarded by Run, for handing to something that will call it
// later — a UI event loop, say.
func Wrap(fn func(), report Reporter) func() {
	return func() { Run(fn, report) }
}

func asError(r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("internal error: %w", err)
	}
	return fmt.Errorf("internal error: %v", r)
}

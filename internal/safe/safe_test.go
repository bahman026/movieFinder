package safe

import (
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

// Panics are logged with their stack; the tests would otherwise spray that
// across the test output.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	m.Run()
}

func TestRunReportsAPanicInsteadOfCrashing(t *testing.T) {
	var got error
	ok := Run(func() { panic("parser fell over") }, func(err error) { got = err })

	if ok {
		t.Error("Run reported success for a function that panicked")
	}
	if got == nil {
		t.Fatal("the reporter was not called")
	}
	if !strings.Contains(got.Error(), "parser fell over") {
		t.Errorf("error = %q, want it to name the panic", got)
	}
}

func TestRunPassesThroughWhenNothingPanics(t *testing.T) {
	called := false
	ok := Run(func() { called = true }, func(error) { t.Error("reporter called for a clean run") })

	if !ok || !called {
		t.Errorf("ok = %v, called = %v; want both true", ok, called)
	}
}

// A panic value that is already an error should stay wrapped, so callers can
// still match it — an index-out-of-range is a runtime.Error, for instance.
func TestRunKeepsAnErrorPanicUnwrappable(t *testing.T) {
	sentinel := errors.New("boom")

	var got error
	Run(func() { panic(sentinel) }, func(err error) { got = err })

	if !errors.Is(got, sentinel) {
		t.Errorf("error = %v, want it to wrap the panicked error", got)
	}
}

// The case this package exists for: a slice index that is only out of range
// for some responses.
func TestRunSurvivesARuntimePanic(t *testing.T) {
	var got error
	Run(func() {
		var empty []string
		_ = empty[3]
	}, func(err error) { got = err })

	if got == nil {
		t.Fatal("an out-of-range index was not recovered")
	}
}

func TestGoRecoversOnItsOwnGoroutine(t *testing.T) {
	reported := make(chan error, 1)
	Go(func() { panic("background work fell over") }, func(err error) { reported <- err })

	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "background work") {
			t.Errorf("error = %q", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reporter was never called")
	}
}

func TestNilReporterIsFine(t *testing.T) {
	if Run(func() { panic("nobody listening") }, nil) {
		t.Error("Run reported success for a function that panicked")
	}
}

func TestWrapGuardsADeferredCall(t *testing.T) {
	var got error
	guarded := Wrap(func() { panic("called later") }, func(err error) { got = err })

	// Whatever holds the function calls it whenever it likes; the point is that
	// the panic does not escape that call.
	guarded()

	if got == nil {
		t.Fatal("the reporter was not called")
	}
}

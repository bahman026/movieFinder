package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectPrefersConfiguredPath(t *testing.T) {
	// A real file on disk stands in for a player executable.
	dir := t.TempDir()
	exe := filepath.Join(dir, "vlc.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := Detect(exe)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if p.Path != exe {
		t.Errorf("Path = %q, want the configured %q", p.Path, exe)
	}
	if p.Name != "VLC" {
		t.Errorf("Name = %q, want VLC (guessed from exe name)", p.Name)
	}
	if p.style != styleSubFile {
		t.Errorf("style = %v, want styleSubFile for VLC", p.style)
	}
}

func TestDetectConfiguredButMissingIsAnError(t *testing.T) {
	_, err := Detect(`C:\nope\does-not-exist.exe`)
	if err == nil {
		t.Fatal("want an error for a missing configured player")
	}
}

// The subtitle argument must match each player's own flag syntax.
func TestSubtitleArgStyles(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "p.exe")
	os.WriteFile(exe, []byte("x"), 0o755)

	sub := filepath.Join(dir, "s.srt")
	cases := []struct {
		style argStyle
		want  string
	}{
		{stylePotSub, "/sub=" + sub},
		{styleSubFile, "--sub-file=" + sub},
		{styleMpcSub, "/sub " + sub},
		{styleIINASub, "--mpv-sub-file=" + sub},
	}
	for _, tc := range cases {
		// subtitleArg is the same helper Play calls, so this covers the real
		// mapping rather than a copy that could drift from it.
		got := subtitleArg(tc.style, sub)
		if !strings.Contains(strings.Join(got, " "), tc.want) {
			t.Errorf("style %v -> %v, want it to contain %q", tc.style, got, tc.want)
		}
	}
}

// A macOS .app install must be found even though it puts nothing on PATH.
func TestDetectFindsMacAppBundlePaths(t *testing.T) {
	for _, want := range []string{
		"/Applications/VLC.app/Contents/MacOS/VLC",
		"/Applications/IINA.app/Contents/MacOS/iina-cli",
		"/opt/homebrew/bin/mpv",
	} {
		var found bool
		for _, c := range candidates {
			for _, p := range c.paths {
				if p == want {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("no candidate probes %q", want)
		}
	}
}

func TestExpandHomeResolvesTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got := expandHome("~/Applications/VLC.app/Contents/MacOS/VLC")
	want := filepath.Join(home, "Applications/VLC.app/Contents/MacOS/VLC")
	if got != want {
		t.Errorf("expandHome = %q, want %q", got, want)
	}
	if got := expandHome("/Applications/VLC.app"); got != "/Applications/VLC.app" {
		t.Errorf("absolute path changed: %q", got)
	}
}

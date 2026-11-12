package mux

import (
	"strings"
	"testing"
)

// The new tracks must be the first subtitle streams: the metadata flags
// address them as s:0, s:1, and would land on the source's own subtitles
// otherwise.
func TestArgsMapNewTracksFirstAndTagThem(t *testing.T) {
	args := strings.Join(Args("Movie.mkv", "out.mkv", []Track{
		{Path: "en.srt", Language: "eng", Title: "English"},
		{Path: "fa.ass", Language: "per", Title: "Persian"},
	}), " ")

	for _, want := range []string{
		"-i Movie.mkv -i en.srt -i fa.ass",
		"-map 0:v? -map 0:a? -map 1:s:0 -map 2:s:0 -map 0:s? -map 0:t?",
		"-c copy",
		"-c:s:0 srt", "-metadata:s:s:0 language=eng", "-metadata:s:s:0 title=English", "-disposition:s:0 default",
		"-c:s:1 ass", "-metadata:s:s:1 language=per", "-disposition:s:1 0",
		"-f matroska out.mkv",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q\n%s", want, args)
		}
	}
}

// An MP4's own subtitles are mov_text, which cannot be stream-copied into MKV.
func TestArgsDropSourceSubtitlesFromNonMatroska(t *testing.T) {
	args := strings.Join(Args("Movie.mp4", "out.mkv", []Track{{Path: "en.srt"}}), " ")
	if strings.Contains(args, "0:s?") || strings.Contains(args, "0:t?") {
		t.Errorf("mp4 input maps its own subtitles: %s", args)
	}
}

func TestToUTF8DecodesWindows1256Persian(t *testing.T) {
	// "سلام" in Windows-1256.
	got := string(ToUTF8([]byte{0xD3, 0xE1, 0xC7, 0xE3}, "fa"))
	if got != "سلام" {
		t.Errorf("got %q, want سلام", got)
	}
}

func TestToUTF8LeavesUTF8AloneAndStripsBOM(t *testing.T) {
	if got := string(ToUTF8([]byte("\xEF\xBB\xBFسلام"), "fa")); got != "سلام" {
		t.Errorf("got %q", got)
	}
	if got := string(ToUTF8([]byte("Hello"), "en")); got != "Hello" {
		t.Errorf("got %q", got)
	}
}

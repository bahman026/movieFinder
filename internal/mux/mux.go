// Package mux embeds subtitle files into a downloaded video as soft subtitle
// tracks, by driving an external ffmpeg.
//
// Same reasoning as internal/player: writing a Matroska muxer that copes with
// whatever container and codecs a file host serves is a project of its own,
// and ffmpeg already does it. Nothing is re-encoded — video and audio are
// stream-copied, so embedding takes seconds and the picture is untouched.
//
// The output is always Matroska. MKV holds SRT/ASS text tracks natively; MP4
// would need them converted to mov_text and many players handle that poorly.
// An .mp4 input therefore becomes Movie.mkv beside where Movie.mp4 was.
package mux

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// Track is one subtitle file to embed.
type Track struct {
	Path string
	// Language is an ISO 639-2 code (eng, per) — what Matroska's language
	// element and every player's track menu expect.
	Language string
	// Title is the track name a player shows, e.g. "English".
	Title string
}

// candidates are where ffmpeg usually lives on Windows besides PATH: the
// manual-unzip spot most guides use, and the shims winget, Chocolatey and
// Scoop install. A GUI app may inherit a PATH from before the install, so
// probing these is what makes a fresh install work without a reboot.
var candidates = []string{
	`C:\ffmpeg\bin\ffmpeg.exe`,
	`C:\Program Files\ffmpeg\bin\ffmpeg.exe`,
	`%LOCALAPPDATA%\Microsoft\WinGet\Links\ffmpeg.exe`,
	`C:\ProgramData\chocolatey\bin\ffmpeg.exe`,
	`%USERPROFILE%\scoop\shims\ffmpeg.exe`,
	"/opt/homebrew/bin/ffmpeg",
	"/usr/local/bin/ffmpeg",
	"/usr/bin/ffmpeg",
}

// Detect finds ffmpeg: the configured path if set, then PATH, then the usual
// install locations.
func Detect(configuredPath string) (string, error) {
	if p := strings.TrimSpace(configuredPath); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("configured ffmpeg not found at %s", p)
	}
	for _, name := range []string{"ffmpeg", "ffmpeg.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	for _, c := range candidates {
		if p := os.ExpandEnv(strings.NewReplacer("%LOCALAPPDATA%", "$LOCALAPPDATA", "%USERPROFILE%", "$USERPROFILE").Replace(c)); fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found — install it (winget install Gyan.FFmpeg) or set its path in Settings")
}

// Args builds the ffmpeg command line that copies videoPath into outPath with
// tracks added as the first subtitle streams.
//
// The new tracks are mapped before the source's own subtitles so their output
// indices are known (s:0, s:1, …) without probing the file first; that is what
// lets the language/title metadata be addressed by index. The source's own
// subtitles and attachments (fonts for ASS) are kept only when it is already
// Matroska — an MP4's mov_text track cannot be stream-copied into MKV.
func Args(videoPath, outPath string, tracks []Track) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", videoPath}
	for _, t := range tracks {
		args = append(args, "-i", t.Path)
	}
	args = append(args, "-map", "0:v?", "-map", "0:a?")
	for i := range tracks {
		args = append(args, "-map", strconv.Itoa(i+1)+":s:0")
	}
	if isMatroska(videoPath) {
		args = append(args, "-map", "0:s?", "-map", "0:t?")
	}
	args = append(args, "-c", "copy")
	for i, t := range tracks {
		idx := strconv.Itoa(i)
		// Re-encoding text subtitles is lossless and normalises formats MKV
		// cannot carry as-is (MicroDVD, WebVTT quirks) into SRT. ASS keeps its
		// styling by staying ASS.
		codec := "srt"
		if ext := strings.ToLower(filepath.Ext(t.Path)); ext == ".ass" || ext == ".ssa" {
			codec = "ass"
		}
		args = append(args, "-c:s:"+idx, codec)
		if t.Language != "" {
			args = append(args, "-metadata:s:s:"+idx, "language="+t.Language)
		}
		if t.Title != "" {
			args = append(args, "-metadata:s:s:"+idx, "title="+t.Title)
		}
		disposition := "0"
		if i == 0 {
			disposition = "default"
		}
		args = append(args, "-disposition:s:"+idx, disposition)
	}
	return append(args, "-f", "matroska", outPath)
}

// Embed adds tracks to videoPath and returns the path of the result, which
// replaces the original: Movie.mkv is rewritten in place, Movie.mp4 becomes
// Movie.mkv and the .mp4 is removed.
//
// ffmpeg writes to a temporary file in the same folder, and the original is
// only touched once that file exists and is about the size of the source — a
// crash, a full disk or a killed process leaves the download as it was.
func Embed(ctx context.Context, ffmpeg, videoPath string, tracks []Track) (string, error) {
	if len(tracks) == 0 {
		return videoPath, nil
	}
	src, err := os.Stat(videoPath)
	if err != nil {
		return "", fmt.Errorf("video file: %w", err)
	}

	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	tmp := base + ".muxing.mkv"
	final := base + ".mkv"

	cmd := exec.CommandContext(ctx, ffmpeg, Args(videoPath, tmp, tracks)...)
	hideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("ffmpeg: %s", lastLine(msg))
	}

	out, err := os.Stat(tmp)
	if err != nil {
		return "", fmt.Errorf("ffmpeg produced no output: %w", err)
	}
	// Stream copy only adds container overhead and subtitles; a result well
	// short of the source means streams were dropped. Keep the original.
	if out.Size() < src.Size()*9/10 {
		os.Remove(tmp)
		return "", fmt.Errorf("ffmpeg output is %d bytes against a %d-byte source; kept the original", out.Size(), src.Size())
	}

	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("replace video: %w", err)
	}
	if !samePath(final, videoPath) {
		os.Remove(videoPath)
	}
	return final, nil
}

// ToUTF8 returns subtitle data as UTF-8 without a BOM.
//
// Persian subtitles in particular are very often Windows-1256, and an MKV
// text track is UTF-8 by definition — copied raw, they show as mojibake in
// every player. Data that is already valid UTF-8 is left alone; anything else
// is decoded from the legacy code page usual for the language.
func ToUTF8(data []byte, lang string) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(data)
		if err == nil {
			return out
		}
	}
	if utf8.Valid(data) {
		return data
	}
	cm := charmap.Windows1252
	switch lang {
	case "fa", "ar", "per", "ara":
		cm = charmap.Windows1256
	case "ru", "rus":
		cm = charmap.Windows1251
	case "tr", "tur":
		cm = charmap.Windows1254
	}
	if out, err := cm.NewDecoder().Bytes(data); err == nil {
		return out
	}
	return data
}

// ISO6392 maps the two-letter codes the subtitle pickers use onto the
// three-letter codes Matroska stores. Unknown codes pass through.
func ISO6392(code string) string {
	switch code {
	case "en":
		return "eng"
	case "fa":
		return "per"
	case "ar":
		return "ara"
	case "fr":
		return "fre"
	case "de":
		return "ger"
	case "es":
		return "spa"
	case "it":
		return "ita"
	case "tr":
		return "tur"
	case "ru":
		return "rus"
	case "pt":
		return "por"
	}
	return code
}

func isMatroska(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mka", ".webm":
		return true
	}
	return false
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

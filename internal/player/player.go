// Package player launches an external desktop video player on a stream URL,
// optionally applying a subtitle file. Playing arbitrary .mkv/HEVC streams and
// muxing an external subtitle is exactly what mpv/VLC/PotPlayer already do well,
// so the app drives one of them rather than embedding a decoder.
package player

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// argStyle is how a given player takes the subtitle argument.
type argStyle int

const (
	styleSubFile argStyle = iota // --sub-file=PATH      (mpv, VLC)
	stylePotSub                  // /sub=PATH            (PotPlayer)
	styleMpcSub                  // /sub PATH            (MPC-HC)
	styleIINASub                 // --mpv-sub-file=PATH  (IINA, via iina-cli)
)

// Player is a launchable external player.
type Player struct {
	Name  string
	Path  string
	style argStyle
}

// candidate is a known player and where it usually lives.
type candidate struct {
	name  string
	style argStyle
	// exeNames are tried on PATH; paths are absolute install locations.
	exeNames []string
	paths    []string
}

// candidates are probed in order — the first found wins. PotPlayer is first
// because it is what is installed here; order otherwise favours the players
// that handle the widest range of codecs out of the box. Locations for every
// OS live in one list: a path that does not exist simply fails its stat, so
// there is nothing to gain from splitting this per platform.
//
// The absolute macOS paths are not redundant with exeNames. A bundled .app
// launched from Finder inherits a minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin)
// rather than the shell's, so exec.LookPath("mpv") misses a Homebrew install
// that works fine from a terminal. Probing the Homebrew prefixes directly is
// what makes detection work in the packaged app.
var candidates = []candidate{
	{
		name:  "PotPlayer",
		style: stylePotSub,
		paths: []string{
			`C:\Program Files\DAUM\PotPlayer\PotPlayerMini64.exe`,
			`C:\Program Files (x86)\DAUM\PotPlayer\PotPlayerMini.exe`,
		},
	},
	{
		name:     "mpv",
		style:    styleSubFile,
		exeNames: []string{"mpv", "mpv.exe"},
		paths: []string{
			`C:\Program Files\mpv\mpv.exe`,
			"/opt/homebrew/bin/mpv", // Homebrew, Apple silicon
			"/usr/local/bin/mpv",    // Homebrew, Intel
			"/Applications/mpv.app/Contents/MacOS/mpv",
			"~/Applications/mpv.app/Contents/MacOS/mpv",
		},
	},
	{
		name:     "mpv.net",
		style:    styleSubFile,
		exeNames: []string{"mpvnet", "mpvnet.exe"},
	},
	{
		name:     "VLC",
		style:    styleSubFile,
		exeNames: []string{"vlc", "vlc.exe"},
		paths: []string{
			`C:\Program Files\VideoLAN\VLC\vlc.exe`,
			`C:\Program Files (x86)\VideoLAN\VLC\vlc.exe`,
			// The .app bundle ships no `vlc` on PATH, so this is the only way
			// a normal macOS VLC install is found.
			"/Applications/VLC.app/Contents/MacOS/VLC",
			"~/Applications/VLC.app/Contents/MacOS/VLC",
		},
	},
	{
		name:  "IINA",
		style: styleIINASub,
		// iina-cli, not the GUI binary beside it: the bundle's IINA executable
		// ignores command-line arguments, so the helper is what accepts a URL.
		paths: []string{
			"/Applications/IINA.app/Contents/MacOS/iina-cli",
			"~/Applications/IINA.app/Contents/MacOS/iina-cli",
		},
	},
	{
		name:  "MPC-HC",
		style: styleMpcSub,
		paths: []string{
			`C:\Program Files\MPC-HC\mpc-hc64.exe`,
			`C:\Program Files (x86)\K-Lite Codec Pack\MPC-HC64\mpc-hc64.exe`,
		},
	},
}

// Detect finds a usable player. A non-empty configuredPath is tried first and,
// if it exists, always wins; its subtitle-argument style is guessed from the
// executable name. Returns an error naming the players it looked for when none
// is found.
func Detect(configuredPath string) (Player, error) {
	if p := strings.TrimSpace(configuredPath); p != "" {
		if fileExists(p) {
			return Player{Name: playerNameFromPath(p), Path: p, style: styleFromPath(p)}, nil
		}
		return Player{}, fmt.Errorf("configured player not found at %s", p)
	}

	for _, c := range candidates {
		for _, name := range c.exeNames {
			if path, err := exec.LookPath(name); err == nil {
				return Player{Name: c.name, Path: path, style: c.style}, nil
			}
		}
		for _, path := range c.paths {
			// Resolved before the stat and carried in Player.Path: a leading ~
			// means nothing to exec.
			if full := expandHome(path); fileExists(full) {
				return Player{Name: c.name, Path: full, style: c.style}, nil
			}
		}
	}
	return Player{}, fmt.Errorf("no video player found — install mpv, VLC, IINA or PotPlayer, or set one in Settings")
}

// expandHome resolves a leading ~/ so candidates can name per-user install
// locations such as ~/Applications.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// Play launches the player on videoURL. When subtitlePath is non-empty it is
// applied via the player's subtitle argument. The player runs independently of
// this process.
func (p Player) Play(videoURL, subtitlePath string) error {
	if strings.TrimSpace(videoURL) == "" {
		return fmt.Errorf("no video URL to play")
	}

	args := []string{videoURL}
	if strings.TrimSpace(subtitlePath) != "" {
		args = append(args, subtitleArg(p.style, subtitlePath)...)
	}

	cmd := exec.Command(p.Path, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", p.Name, err)
	}
	// Do not Wait — the player owns its lifetime from here.
	go func() { _ = cmd.Wait() }() // reap so it does not linger as a zombie
	return nil
}

// subtitleArg is each player's own flag syntax for an external subtitle file.
// Play uses it rather than an inline switch so the test exercises the real
// mapping instead of a copy of it.
func subtitleArg(style argStyle, sub string) []string {
	switch style {
	case stylePotSub:
		return []string{"/sub=" + sub}
	case styleMpcSub:
		return []string{"/sub", sub}
	case styleIINASub:
		return []string{"--mpv-sub-file=" + sub}
	default: // styleSubFile
		return []string{"--sub-file=" + sub}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func playerNameFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "potplayer"):
		return "PotPlayer"
	case strings.Contains(lower, "mpvnet"):
		return "mpv.net"
	case strings.Contains(lower, "mpv"):
		return "mpv"
	case strings.Contains(lower, "vlc"):
		return "VLC"
	case strings.Contains(lower, "mpc-hc"):
		return "MPC-HC"
	case strings.Contains(lower, "iina"):
		return "IINA"
	}
	return "player"
}

func styleFromPath(path string) argStyle {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "potplayer"):
		return stylePotSub
	case strings.Contains(lower, "mpc-hc"):
		return styleMpcSub
	case strings.Contains(lower, "iina"):
		return styleIINASub
	default:
		return styleSubFile
	}
}

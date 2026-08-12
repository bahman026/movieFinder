// Package player launches an external desktop video player on a stream URL,
// optionally applying a subtitle file. Playing arbitrary .mkv/HEVC streams and
// muxing an external subtitle is exactly what mpv/VLC/PotPlayer already do well,
// so the app drives one of them rather than embedding a decoder.
package player

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// argStyle is how a given player takes the subtitle argument.
type argStyle int

const (
	styleSubFile argStyle = iota // --sub-file=PATH  (mpv, VLC)
	stylePotSub                  // /sub=PATH        (PotPlayer)
	styleMpcSub                  // /sub PATH        (MPC-HC)
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
// that handle the widest range of codecs out of the box.
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
			if fileExists(path) {
				return Player{Name: c.name, Path: path, style: c.style}, nil
			}
		}
	}
	return Player{}, fmt.Errorf("no video player found — install mpv, VLC or PotPlayer, or set one in Settings")
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
		switch p.style {
		case stylePotSub:
			args = append(args, "/sub="+subtitlePath)
		case styleMpcSub:
			args = append(args, "/sub", subtitlePath)
		default: // styleSubFile
			args = append(args, "--sub-file="+subtitlePath)
		}
	}

	cmd := exec.Command(p.Path, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", p.Name, err)
	}
	// Do not Wait — the player owns its lifetime from here.
	go func() { _ = cmd.Wait() }() // reap so it does not linger as a zombie
	return nil
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
	default:
		return styleSubFile
	}
}

package ui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon.png
var iconPNG []byte

// appIcon is the window/taskbar icon, embedded into the binary.
var appIcon = fyne.NewStaticResource("icon.png", iconPNG)

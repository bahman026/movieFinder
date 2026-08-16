package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// movieTheme is the app's own dark palette, in the vein of the streaming sites
// this client browses: a near-black ground, cards a shade above it, one warm
// accent, and body text that drops to grey rather than to a second colour.
//
// It is deliberately dark in both variants. Fyne hands us the OS light/dark
// preference in v, and we ignore it: posters are the content here, and a light
// ground makes a wall of artwork look washed out. Committing to one palette
// also means the contrast below is the contrast that ships, rather than
// something only checked in whichever mode the developer happened to use.
type movieTheme struct{}

var _ fyne.Theme = movieTheme{}

// The palette. Greys carry a slight blue cast so posters (which are rarely
// neutral) sit on it without the ground looking muddy by comparison.
var (
	colorGround    = color.NRGBA{R: 0x0F, G: 0x11, B: 0x16, A: 0xFF} // window background
	colorSurface   = color.NRGBA{R: 0x1A, G: 0x1D, B: 0x24, A: 0xFF} // cards, inputs, buttons
	colorSurfaceUp = color.NRGBA{R: 0x22, G: 0x26, B: 0x2F, A: 0xFF} // hover, raised
	colorLine      = color.NRGBA{R: 0x2A, G: 0x2F, B: 0x3A, A: 0xFF} // separators, borders

	colorText  = color.NRGBA{R: 0xEC, G: 0xED, B: 0xEF, A: 0xFF} // primary text
	colorMuted = color.NRGBA{R: 0x9A, G: 0xA1, B: 0xAD, A: 0xFF} // secondary text
	// colorFaded is the disabled colour. Fyne's own is 1.4:1 against the input
	// background, which renders disabled text invisible rather than merely
	// quiet; this is ~4.5:1, still clearly inactive but readable.
	colorFaded = color.NRGBA{R: 0x8C, G: 0x93, B: 0xA0, A: 0xFF}

	// One warm accent, the colour these sites give ratings and calls to action.
	colorAccent   = color.NRGBA{R: 0xF5, G: 0xB3, B: 0x01, A: 0xFF}
	colorOnAccent = color.NRGBA{R: 0x14, G: 0x11, B: 0x00, A: 0xFF}

	colorGood    = color.NRGBA{R: 0x3F, G: 0xB9, B: 0x50, A: 0xFF}
	colorWarn    = color.NRGBA{R: 0xE8, G: 0xA3, B: 0x17, A: 0xFF}
	colorBad     = color.NRGBA{R: 0xE1, G: 0x4C, B: 0x3C, A: 0xFF}
	colorScrim   = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x99} // over posters
	colorOverlay = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xB0} // behind dialogs
)

func (movieTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colorGround
	case theme.ColorNameForeground, theme.ColorNameForegroundOnPrimary:
		if n == theme.ColorNameForegroundOnPrimary {
			return colorOnAccent
		}
		return colorText
	case theme.ColorNameInputBackground, theme.ColorNameButton, theme.ColorNameOverlayBackground,
		theme.ColorNameMenuBackground:
		return colorSurface
	case theme.ColorNameHover, theme.ColorNameFocus, theme.ColorNameSelection,
		theme.ColorNameDisabledButton, theme.ColorNamePressed:
		return colorSurfaceUp
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return colorLine
	case theme.ColorNamePlaceHolder:
		return colorMuted
	case theme.ColorNameDisabled:
		return colorFaded
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return colorAccent
	case theme.ColorNameSuccess:
		return colorGood
	case theme.ColorNameWarning:
		return colorWarn
	case theme.ColorNameError:
		return colorBad
	case theme.ColorNameShadow:
		return colorOverlay
	case theme.ColorNameScrollBar:
		return colorLine
	}
	// Anything not named above falls back to the built-in dark values, so a
	// colour added in a later Fyne release still renders sensibly.
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (movieTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }

func (movieTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (movieTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNamePadding:
		return 6 // a touch more air between controls than the default 4
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInputRadius:
		return 8 // rounded fields and buttons, as on the reference sites
	case theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 16
	}
	return theme.DefaultTheme().Size(n)
}

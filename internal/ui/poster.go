package ui

import (
	"image/color"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/api"
)

// Card geometry. The poster is a 2:3 portrait, matching what the API serves.
// The extra height under it carries two lines: the title and, beneath it in
// grey, the localized title — the pairing the reference sites use.
const (
	cardWidth    float32 = 158
	posterHeight float32 = 237
	cardHeight           = posterHeight + 62

	badgeRadius float32 = 9 // pill badges over the poster
	cardRadius  float32 = 10
)

var (
	// Badge text is near-black on every badge colour: all three rating colours
	// are light enough that white text on them falls below readable contrast.
	badgeText = color.NRGBA{R: 0x10, G: 0x12, B: 0x16, A: 0xFF}
	// The year sits in a dark scrim rather than a colour, so only the rating
	// competes with the artwork for attention.
	yearText = color.NRGBA{R: 0xEC, G: 0xED, B: 0xEF, A: 0xFF}
)

// ratingFill colours a score the way the reference sites do: green is good,
// amber is middling, red is poor.
func ratingFill(rating string) color.Color {
	switch v, err := strconv.ParseFloat(strings.TrimSpace(rating), 64); {
	case err != nil:
		return colorMuted
	case v >= 7:
		return colorGood
	case v >= 5:
		return colorWarn
	default:
		return colorBad
	}
}

// imageCache holds decoded posters so scrolling back up does not refetch them.
type imageCache struct {
	mu      sync.Mutex
	entries map[string]fyne.Resource
}

func newImageCache() *imageCache {
	return &imageCache{entries: make(map[string]fyne.Resource)}
}

func (c *imageCache) get(url string) (fyne.Resource, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, ok := c.entries[url]
	return res, ok
}

func (c *imageCache) put(url string, res fyne.Resource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Browsing far enough could otherwise grow this without bound. Posters are
	// cheap to refetch, so dropping the whole map is fine.
	if len(c.entries) > 400 {
		c.entries = make(map[string]fyne.Resource, 64)
	}
	c.entries[url] = res
}

// posterCard is one grid tile: poster, rating and year badges, title.
//
// The grid recycles cards as it scrolls, so each card records the URL it is
// currently meant to show and a late-arriving image is discarded if the card
// has since been reused for a different title.
type posterCard struct {
	widget.BaseWidget

	root   *fyne.Container
	poster *canvas.Image

	ratingBox  *fyne.Container
	ratingRect *canvas.Rectangle
	ratingText *canvas.Text
	yearBox    *fyne.Container
	yearText   *canvas.Text
	title      *widget.Label
	subtitle   *widget.Label

	mu   sync.Mutex
	want string
}

func newPosterCard() *posterCard {
	// A rounded plate behind the artwork. It shows while a poster is loading
	// and as a border around any image that does not fill the 2:3 box, so a
	// half-loaded grid reads as a grid of cards rather than of holes.
	plate := canvas.NewRectangle(colorSurface)
	plate.CornerRadius = cardRadius

	poster := canvas.NewImageFromResource(theme.BrokenImageIcon())
	poster.FillMode = canvas.ImageFillContain
	poster.SetMinSize(fyne.NewSize(cardWidth, posterHeight))

	ratingText := canvas.NewText("", badgeText)
	ratingText.TextSize = 12
	ratingText.TextStyle = fyne.TextStyle{Bold: true}
	ratingRect := canvas.NewRectangle(colorGood)
	ratingRect.CornerRadius = badgeRadius
	ratingBox := container.NewStack(ratingRect, container.NewPadded(ratingText))

	yearLabel := canvas.NewText("", yearText)
	yearLabel.TextSize = 11
	yearRect := canvas.NewRectangle(colorScrim)
	yearRect.CornerRadius = badgeRadius
	yearBox := container.NewStack(yearRect, container.NewPadded(yearLabel))

	title := widget.NewLabel("")
	title.Alignment = fyne.TextAlignCenter
	title.Truncation = fyne.TextTruncateEllipsis
	title.TextStyle = fyne.TextStyle{Bold: true}

	// The localized title, quieter and smaller beneath the main one.
	subtitle := widget.NewLabel("")
	subtitle.Alignment = fyne.TextAlignCenter
	subtitle.Truncation = fyne.TextTruncateEllipsis
	subtitle.Importance = widget.LowImportance
	subtitle.SizeName = theme.SizeNameCaptionText

	// Badges ride along the poster's top edge, rating on the left, as on the
	// reference grids. The spacer under them keeps the row pinned to the top.
	overlay := container.NewVBox(
		container.NewHBox(ratingBox, layout.NewSpacer(), yearBox),
		layout.NewSpacer(),
	)

	card := &posterCard{
		poster:     poster,
		ratingBox:  ratingBox,
		ratingRect: ratingRect,
		ratingText: ratingText,
		yearBox:    yearBox,
		yearText:   yearLabel,
		title:      title,
		subtitle:   subtitle,
	}
	// Negative spacing pulls the two lines into one block. A plain VBox would
	// stack each Label's own inner padding on top of the box padding and leave
	// the localized title floating a long way under the title it belongs to.
	caption := container.New(layout.NewCustomPaddedVBoxLayout(-13), title, subtitle)

	card.root = container.NewBorder(
		nil, caption, nil, nil,
		container.NewStack(plate, poster, overlay),
	)
	card.ExtendBaseWidget(card)
	return card
}

// CreateRenderer makes posterCard a real widget, so the grid's update callback
// can type-assert the item back to it instead of keeping a side table.
func (c *posterCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.root)
}

// set fills the card with a title and kicks off the poster fetch.
func (c *posterCard) set(movie api.Movie, load func(url string, apply func(fyne.Resource))) {
	c.title.SetText(movie.Title)

	// Writer is the localized title despite the name; showing it under the main
	// title is the same pairing the reference grids use. Blank when it merely
	// repeats the title, so the card does not say the same thing twice.
	if sub := strings.TrimSpace(movie.Writer); sub != "" && sub != strings.TrimSpace(movie.Title) {
		c.subtitle.SetText(sub)
		c.subtitle.Show()
	} else {
		c.subtitle.SetText("")
		c.subtitle.Hide()
	}

	if rating := movie.IMDBRating; rating != "" && rating != "0" {
		c.ratingText.Text = rating
		c.ratingText.Refresh()
		c.ratingRect.FillColor = ratingFill(rating)
		c.ratingRect.Refresh()
		c.ratingBox.Show()
	} else {
		c.ratingBox.Hide()
	}

	if year := movie.Year(); year != "" {
		c.yearText.Text = year
		c.yearText.Refresh()
		c.yearBox.Show()
	} else {
		c.yearBox.Hide()
	}

	url := movie.PosterURL
	if url == "" {
		url = movie.ThumbnailURL
	}

	c.mu.Lock()
	c.want = url
	c.mu.Unlock()

	c.poster.Resource = theme.BrokenImageIcon()
	c.poster.Refresh()

	if url == "" {
		return
	}
	load(url, func(res fyne.Resource) {
		// Only paint if this card still wants this image.
		c.mu.Lock()
		stale := c.want != url
		c.mu.Unlock()
		if stale {
			return
		}
		c.poster.Resource = res
		c.poster.Refresh()
	})
}

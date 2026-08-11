package ui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/adlas/moviefinder/internal/api"
)

// Card geometry. The poster is a 2:3 portrait, matching what the API serves.
const (
	cardWidth    float32 = 150
	posterHeight float32 = 225
	cardHeight           = posterHeight + 46 // room for the title underneath
)

var (
	ratingColor = color.NRGBA{R: 0xF0, G: 0x93, B: 0x00, A: 0xFF} // amber
	yearColor   = color.NRGBA{R: 0x14, G: 0x65, B: 0xC0, A: 0xFF} // blue
	badgeText   = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
)

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
	ratingText *canvas.Text
	yearBox    *fyne.Container
	yearText   *canvas.Text
	title      *widget.Label

	mu   sync.Mutex
	want string
}

func newPosterCard() *posterCard {
	poster := canvas.NewImageFromResource(theme.BrokenImageIcon())
	poster.FillMode = canvas.ImageFillContain
	poster.SetMinSize(fyne.NewSize(cardWidth, posterHeight))

	ratingText := canvas.NewText("", badgeText)
	ratingText.TextSize = 12
	ratingText.TextStyle = fyne.TextStyle{Bold: true}
	ratingBox := badge(ratingText, ratingColor)

	yearText := canvas.NewText("", badgeText)
	yearText.TextSize = 12
	yearText.TextStyle = fyne.TextStyle{Bold: true}
	yearBox := badge(yearText, yearColor)

	title := widget.NewLabel("")
	title.Alignment = fyne.TextAlignCenter
	title.Truncation = fyne.TextTruncateEllipsis

	// The badge row sits on top of the poster, pushed to its bottom edge.
	overlay := container.NewVBox(
		layout.NewSpacer(),
		container.NewHBox(ratingBox, layout.NewSpacer(), yearBox),
	)

	card := &posterCard{
		poster:     poster,
		ratingBox:  ratingBox,
		ratingText: ratingText,
		yearBox:    yearBox,
		yearText:   yearText,
		title:      title,
	}
	card.root = container.NewBorder(nil, title, nil, nil, container.NewStack(poster, overlay))
	card.ExtendBaseWidget(card)
	return card
}

// CreateRenderer makes posterCard a real widget, so the grid's update callback
// can type-assert the item back to it instead of keeping a side table.
func (c *posterCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.root)
}

func badge(text *canvas.Text, fill color.Color) *fyne.Container {
	rect := canvas.NewRectangle(fill)
	return container.NewStack(rect, container.NewPadded(text))
}

// set fills the card with a title and kicks off the poster fetch.
func (c *posterCard) set(movie api.Movie, load func(url string, apply func(fyne.Resource))) {
	c.title.SetText(movie.Title)

	if rating := movie.IMDBRating; rating != "" && rating != "0" {
		c.ratingText.Text = rating
		c.ratingText.Refresh()
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

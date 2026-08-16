package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"moviefinder/internal/api"
)

// Renders the UI to PNGs so the design can be looked at, rather than reasoned
// about. Fyne's test driver paints in software, so this needs no display — but
// it is still opt-in, because its output is images for a human, not assertions:
//
//	UI_PREVIEW=1 go test ./internal/ui -run TestPreview
//
// The files land in internal/ui/testdata/preview (gitignored).
func TestPreview(t *testing.T) {
	if os.Getenv("UI_PREVIEW") == "" {
		t.Skip("set UI_PREVIEW=1 to render UI previews")
	}

	app := test.NewApp()
	defer test.NewApp() // reset the global app for any later test
	app.Settings().SetTheme(movieTheme{})

	outDir := filepath.Join("testdata", "preview")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	shot := func(name string, size fyne.Size, content fyne.CanvasObject) {
		t.Helper()
		w := test.NewWindow(content)
		defer w.Close()
		w.Resize(size)

		img := w.Canvas().Capture()
		f, err := os.Create(filepath.Join(outDir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s.png", name)
	}

	shot("grid", fyne.NewSize(700, 640), previewGrid())
	// The detail page is full-window now, so preview it at window size.
	shot("detail", fyne.NewSize(1100, 700), previewDetail())
	shot("links", fyne.NewSize(620, 320), previewLinks())
}

// previewGrid lays out sample cards the way the listing does.
func previewGrid() fyne.CanvasObject {
	samples := []api.Movie{
		{Title: "The Eyes", Writer: "چشم ها", IMDBRating: "8.2", Release: "2026"},
		{Title: "Young Washington", Writer: "واشینگتن جوان", IMDBRating: "6.4", Release: "2025"},
		{Title: "Nando Entre Dois Mundos", Writer: "ناندو میان دو دنیا", IMDBRating: "4.9", Release: "2024"},
		{Title: "Artemis to the Moon and Back", Writer: "سفر آرتمیس ۲ به ماه", IMDBRating: "7.7", Release: "2026"},
		{Title: "Tighee", Writer: "راز خانوادگی", IMDBRating: "", Release: "2025"},
		{Title: "All Night Wrong", Writer: "تمام شب در اشتباه", IMDBRating: "5.1", Release: "2026"},
	}

	cards := make([]fyne.CanvasObject, 0, len(samples))
	for _, m := range samples {
		c := newPosterCard()
		// No network in a preview: fill the plate with a flat colour so the
		// badges and captions can be judged against something poster-shaped.
		c.set(m, func(string, func(fyne.Resource)) {})
		c.poster.Resource = nil
		cards = append(cards, container.NewPadded(c))
	}

	grid := container.NewGridWithColumns(4, cards...)
	return container.NewPadded(grid)
}

func previewDetail() fyne.CanvasObject {
	u := &UI{}
	pane := u.buildDetailPane()

	u.setDetailHeader(api.Detail{
		Title:        "Artemis to the Moon and Back",
		Writer:       "سفر آرتمیس ۲ به ماه",
		IMDBRating:   "7.7",
		Release:      "2026",
		Runtime:      "59 min",
		VideoQuality: "WEB-DL",
	})
	u.infoText = "Genre: Documentary\nCountry: England\nDirector: Jane Doe\nCast: A. Person, B. Person, C. Person\n\n" +
		"Artemis 2026 follows four NASA astronauts from preparation through lunar orbit and back to Earth, " +
		"with behind-the-scenes access to the crew and to mission control."
	u.info.SetText(u.infoText)

	// Fill the links column too, so the two halves can be judged together.
	u.showLinks([]api.DownloadLink{
		{Label: "720P زیرنویس", URL: "https://dl20bt.example.net/Artemis.2026.720p.mkv", FileSize: "1400"},
		{Label: "1080P زیرنویس", URL: "https://dl20bt.example.net/Artemis.2026.1080p.mkv", FileSize: "3100"},
	})
	return pane
}

func previewLinks() fyne.CanvasObject {
	u := &UI{}
	rows := container.NewVBox(
		u.linkRow(api.DownloadLink{Label: "720P زیرنویس", URL: "https://dl3.example.net/The.Eyes.2026.720p.mkv", FileSize: "1400"}),
		u.linkRow(api.DownloadLink{Label: "4K X265 زیرنویس", URL: "https://dl3.example.net/The.Eyes.2026.2160p.mkv", FileSize: "5200"}),
	)
	return container.NewPadded(rows)
}

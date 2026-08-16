package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"moviefinder/internal/api"
	"moviefinder/internal/config"
	"moviefinder/internal/delfan"
	"moviefinder/internal/mysubs"
	"moviefinder/internal/opensubtitles"
)

// showSettings opens the configuration dialog and applies the result.
func (u *UI) showSettings() {
	cfg := u.cfg

	hosts := widget.NewMultiLineEntry()
	hosts.SetText(strings.Join(cfg.CleanHosts(), "\n"))
	hosts.SetPlaceHolder("http://host-one\nhttp://host-two")
	hosts.SetMinRowsVisible(3)

	basePath := entryWith(cfg.BasePath, "/playstore/api")
	secretKey := entryWith(cfg.APISecretKey, "api_secret_key")
	version := entryWith(cfg.Version, "4.5.0")
	country := entryWith(cfg.Country, "other")
	timeout := entryWith(strconv.Itoa(cfg.TimeoutSeconds), "30")

	sp := widget.NewCheck("Send sp=true", nil)
	sp.SetChecked(cfg.SP)

	insecure := widget.NewCheck("Skip TLS certificate verification", nil)
	insecure.SetChecked(cfg.InsecureTLS)

	// Override for the built-in OpenSubtitles key. Shows only the user's own
	// value (never the baked-in default); blank means "use the built-in key".
	// A stored value equal to the default is treated as no override, so the
	// default can never get stuck showing in the field.
	subtitlesKey := widget.NewPasswordEntry()
	if cfg.OpenSubtitlesAPIKey != opensubtitles.DefaultAPIKey {
		subtitlesKey.SetText(cfg.OpenSubtitlesAPIKey)
	}
	subtitlesKey.SetPlaceHolder("leave blank to use the built-in key")

	// Which source the subtitle pickers open on. Both stay available in the
	// dialogs themselves; this only sets the starting choice.
	subtitleSource := widget.NewSelect(sourceLabels(), nil)
	subtitleSource.SetSelected(sourceLabel(cfg.SubtitleProvider))

	mysubsHost := entryWith(cfg.MySubsBaseURL, mysubs.DefaultBaseURL+" (default)")

	// Database 2's whole endpoint shape, not just its domains: this server
	// rotates hosts and has changed its URL layout between app releases, so
	// each piece is editable and blank means "use the built-in default".
	delfanLoginHost := entryWith(cfg.DelfanLoginHost, delfan.DefaultLoginHost+" (default)")
	delfanAPIHost := entryWith(cfg.DelfanAPIHost, delfan.DefaultAPIHost+" (default)")
	delfanBasePath := entryWith(cfg.DelfanBasePath, delfan.DefaultBasePath+" (default)")
	delfanLoginEndpoint := entryWith(cfg.DelfanLoginEndpoint, delfan.DefaultLoginEndpoint+" (default)")
	delfanAPIEndpoint := entryWith(cfg.DelfanAPIEndpoint, delfan.DefaultAPIEndpoint+" (default)")
	delfanAPIKey := entryWith(cfg.DelfanAPIKey, "leave blank for the built-in key")
	delfanAppVersion := entryWith(cfg.DelfanAppVersion, delfan.DefaultAppVersion+" (default)")

	playerPath := entryWith(cfg.PlayerPath, "auto-detect (PotPlayer, mpv, VLC, MPC-HC)")

	// collect turns the current field values into a Config.
	collect := func() config.Config {
		next := cfg
		next.Hosts = strings.FieldsFunc(hosts.Text, func(r rune) bool {
			return r == '\n' || r == '\r'
		})
		next.BasePath = strings.TrimRight(strings.TrimSpace(basePath.Text), "/")
		next.APISecretKey = strings.TrimSpace(secretKey.Text)
		next.Version = strings.TrimSpace(version.Text)
		next.Country = strings.TrimSpace(country.Text)
		next.SP = sp.Checked
		next.InsecureTLS = insecure.Checked
		// Store the override, but never persist the default itself — an empty
		// value already means "use the default".
		if key := strings.TrimSpace(subtitlesKey.Text); key != opensubtitles.DefaultAPIKey {
			next.OpenSubtitlesAPIKey = key
		} else {
			next.OpenSubtitlesAPIKey = ""
		}
		next.SubtitleProvider = sourceCode(subtitleSource.Selected)
		next.MySubsBaseURL = strings.TrimRight(strings.TrimSpace(mysubsHost.Text), "/")
		next.DelfanLoginHost = strings.TrimSpace(delfanLoginHost.Text)
		next.DelfanAPIHost = strings.TrimSpace(delfanAPIHost.Text)
		next.DelfanBasePath = strings.TrimSpace(delfanBasePath.Text)
		next.DelfanLoginEndpoint = strings.TrimSpace(delfanLoginEndpoint.Text)
		next.DelfanAPIEndpoint = strings.TrimSpace(delfanAPIEndpoint.Text)
		next.DelfanAPIKey = strings.TrimSpace(delfanAPIKey.Text)
		next.DelfanAppVersion = strings.TrimSpace(delfanAppVersion.Text)
		next.PlayerPath = strings.TrimSpace(playerPath.Text)
		if n, err := strconv.Atoi(timeout.Text); err == nil && n > 0 {
			next.TimeoutSeconds = n
		}
		return next
	}

	testResult := widget.NewLabel("")
	testResult.Wrapping = fyne.TextWrapWord
	testButton := widget.NewButton("Test each host", nil)
	testButton.OnTapped = func() {
		candidate := collect()
		if err := candidate.Validate(); err != nil {
			testResult.SetText("x " + err.Error())
			return
		}

		testButton.Disable()
		testResult.SetText("Testing…")

		u.bg("The host test", func() {
			// Test every host individually rather than through the failover
			// path, so a working mirror cannot hide a broken one.
			var lines []string
			for _, host := range candidate.CleanHosts() {
				single := candidate
				single.Hosts = []string{host}

				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				movies, err := api.New(single).Movies(ctx, 1)
				cancel()

				if err != nil {
					lines = append(lines, "x "+host+" — "+firstLine(err.Error()))
					continue
				}
				lines = append(lines, fmt.Sprintf("ok %s — %d title(s)", host, len(movies)))
			}

			u.onUI("The host test", func() {
				testButton.Enable()
				testResult.SetText(strings.Join(lines, "\n"))
			})
		})
	}

	// Database 2 has no host list to walk, so its check is a single real call
	// through the whole signed session: login, seed the nonce, fetch. That is
	// the only way to tell a good endpoint shape from a bad one.
	delfanResult := widget.NewLabel("")
	delfanResult.Wrapping = fyne.TextWrapWord
	delfanTest := widget.NewButton("Test Database 2", nil)
	delfanTest.OnTapped = func() {
		candidate := collect()
		delfanTest.Disable()
		delfanResult.SetText("Testing…")

		u.bg("The Database 2 test", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			items, err := delfan.NewWithOptions(delfanOptions(candidate)).Home(ctx)
			cancel()

			u.onUI("The Database 2 test", func() {
				delfanTest.Enable()
				if err != nil {
					delfanResult.SetText("x " + firstLine(err.Error()))
					return
				}
				delfanResult.SetText(fmt.Sprintf("ok — %d title(s)", len(items)))
			})
		})
	}

	form := []*widget.FormItem{
		{Text: "", Widget: settingsHeading("Database 1")},
		{Text: "Hosts", Widget: hosts, HintText: "One per line, tried top to bottom. The first that answers is used until it fails."},
		{Text: "Base path", Widget: basePath},
		{Text: "API secret key", Widget: secretKey},
		{Text: "Version", Widget: version},
		{Text: "Country", Widget: country},
		{Text: "", Widget: sp},
		{Text: "", Widget: insecure, HintText: "Only needed for https:// hosts, which currently serve a certificate for a different name."},
		{Text: "Timeout (s)", Widget: timeout},
		{Text: "", Widget: container.NewVBox(testButton, testResult)},

		{Text: "", Widget: settingsHeading("Database 2")},
		{Text: "Login host", Widget: delfanLoginHost, HintText: "Every field here may be left blank to use the built-in default. Change them if Database 2 moves or its URLs change."},
		{Text: "API host", Widget: delfanAPIHost},
		{Text: "Base path", Widget: delfanBasePath, HintText: "Path prefix shared by both hosts."},
		{Text: "Login endpoint", Widget: delfanLoginEndpoint},
		{Text: "API endpoint", Widget: delfanAPIEndpoint, HintText: "File name serving the gated actions."},
		{Text: "API key", Widget: delfanAPIKey, HintText: "The key= parameter sent on every request."},
		{Text: "App version", Widget: delfanAppVersion, HintText: "Folded into the signed request body. Change only if the server starts rejecting a correct host."},
		{Text: "", Widget: container.NewVBox(delfanTest, delfanResult)},

		{Text: "", Widget: settingsHeading("Subtitles")},
		{Text: "Default source", Widget: subtitleSource, HintText: "Both sources stay selectable in the Subtitles and Play dialogs; this is only the one they open on."},
		{Text: "OpenSubtitles key", Widget: subtitlesKey, HintText: "Optional — overrides the built-in key. Get your own at " + opensubtitles.RegisterURL},
		{Text: "MySubs address", Widget: mysubsHost, HintText: "Blank uses " + mysubs.DefaultBaseURL + ". No key or account is needed; the site is scraped."},

		{Text: "", Widget: settingsHeading("Other")},
		{Text: "Video player", Widget: playerPath, HintText: "Path to a player exe, or blank to auto-detect. Play passes the stream and subtitle to it."},
	}

	// A plain form inside a scroller rather than dialog.NewForm: the form dialog
	// does not scroll its content, so as soon as the fields are taller than the
	// dialog the Save/Cancel row is drawn over the last input instead of below
	// it. There are enough fields here that this happens at any reasonable
	// dialog height, and macOS's larger text metrics make it worse. Scrolling
	// the fields keeps the buttons clear of them at every size.
	content := container.NewVScroll(widget.NewForm(form...))

	d := dialog.NewCustomConfirm("Settings", "Save", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		next := collect()
		if err := next.Validate(); err != nil {
			dialog.ShowError(err, u.window)
			return
		}
		if err := config.Save(next); err != nil {
			dialog.ShowError(fmt.Errorf("could not save settings: %w", err), u.window)
			return
		}

		u.cfg = next
		u.client = api.New(next)
		u.delfan = delfan.NewWithOptions(delfanOptions(next))
		u.subtitles = opensubtitles.New(opensubtitles.ResolveKey(next.OpenSubtitlesAPIKey))
		u.mysubs = mysubs.New(next.MySubsBaseURL)
		u.setStatus("Settings saved.")
		u.reload(1)
	}, u.window)

	// Clamp to the window: a fixed 600 is taller than a laptop screen once the
	// window itself is small, which would push the buttons out of reach.
	win := u.window.Canvas().Size()
	d.Resize(fyne.NewSize(
		fyne.Min(660, win.Width-40),
		fyne.Min(600, win.Height-40),
	))
	d.Show()
}

// settingsHeading names a group of fields, so the two databases are not one
// undifferentiated wall of inputs.
func settingsHeading(text string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return container.NewVBox(widget.NewSeparator(), label)
}

func entryWith(value, placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(value)
	e.SetPlaceHolder(placeholder)
	return e
}

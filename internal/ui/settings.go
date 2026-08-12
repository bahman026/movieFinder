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

	delfanLoginHost := entryWith(cfg.DelfanLoginHost, delfan.DefaultLoginHost+" (default)")
	delfanAPIHost := entryWith(cfg.DelfanAPIHost, delfan.DefaultAPIHost+" (default)")

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
		next.DelfanLoginHost = strings.TrimSpace(delfanLoginHost.Text)
		next.DelfanAPIHost = strings.TrimSpace(delfanAPIHost.Text)
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

		go func() {
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

			fyne.Do(func() {
				testButton.Enable()
				testResult.SetText(strings.Join(lines, "\n"))
			})
		}()
	}

	form := []*widget.FormItem{
		{Text: "Hosts", Widget: hosts, HintText: "One per line, tried top to bottom. The first that answers is used until it fails."},
		{Text: "Base path", Widget: basePath},
		{Text: "API secret key", Widget: secretKey},
		{Text: "Version", Widget: version},
		{Text: "Country", Widget: country},
		{Text: "", Widget: sp},
		{Text: "", Widget: insecure, HintText: "Only needed for https:// hosts, which currently serve a certificate for a different name."},
		{Text: "Timeout (s)", Widget: timeout},
		{Text: "", Widget: container.NewVBox(testButton, testResult)},
		{Text: "OpenSubtitles key", Widget: subtitlesKey, HintText: "Optional — overrides the built-in key. Get your own at " + opensubtitles.RegisterURL},
		{Text: "Delfan login host", Widget: delfanLoginHost, HintText: "Leave blank for the default. Change if the Delfan source stops working."},
		{Text: "Delfan API host", Widget: delfanAPIHost},
		{Text: "Video player", Widget: playerPath, HintText: "Path to a player exe, or blank to auto-detect. Play passes the stream and subtitle to it."},
	}

	d := dialog.NewForm("Settings", "Save", "Cancel", form, func(ok bool) {
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
		u.delfan = delfan.New(next.DelfanLoginHost, next.DelfanAPIHost)
		u.subtitles = opensubtitles.New(opensubtitles.ResolveKey(next.OpenSubtitlesAPIKey))
		u.setStatus("Settings saved.")
		u.reload(1)
	}, u.window)

	d.Resize(fyne.NewSize(660, 600))
	d.Show()
}

func entryWith(value, placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(value)
	e.SetPlaceHolder(placeholder)
	return e
}

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

	"github.com/adlas/moviefinder/internal/api"
	"github.com/adlas/moviefinder/internal/config"
	"github.com/adlas/moviefinder/internal/opensubtitles"
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

	subtitlesKey := widget.NewPasswordEntry()
	subtitlesKey.SetText(cfg.OpenSubtitlesAPIKey)
	subtitlesKey.SetPlaceHolder("get a free key at " + opensubtitles.RegisterURL)

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
		next.OpenSubtitlesAPIKey = strings.TrimSpace(subtitlesKey.Text)
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
		{Text: "OpenSubtitles key", Widget: subtitlesKey, HintText: "Needed for the Subtitles button. Free account + \"consumer\" app at " + opensubtitles.RegisterURL},
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
		u.subtitles = opensubtitles.New(next.OpenSubtitlesAPIKey)
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

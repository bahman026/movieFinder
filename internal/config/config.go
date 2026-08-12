// Package config loads and stores the client settings on the local machine.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config is the full set of user settings, persisted as JSON.
type Config struct {
	// Hosts are tried in order. The client pins whichever one answers and
	// moves to the next only when that one stops responding, so a dead
	// mirror costs one failed request rather than one per call.
	//
	// Scheme included, no trailing slash. HTTP is the default because the
	// servers present a certificate that does not match either hostname;
	// see InsecureTLS.
	Hosts []string `json:"hosts"`

	// BasePath is the path prefix shared by every endpoint.
	BasePath string `json:"base_path"`

	// Query parameters the API expects on every request.
	APISecretKey string `json:"api_secret_key"`
	Version      string `json:"version"`
	Country      string `json:"country"`
	SP           bool   `json:"sp"`

	// InsecureTLS skips certificate verification. Needed only if you switch
	// the hosts above to https:// — both servers currently answer with a
	// certificate issued for a different name, so verification always fails.
	// Leaving the hosts on http:// is the honest option: neither setting
	// gives you an authenticated connection to these servers.
	InsecureTLS bool `json:"insecure_tls"`

	TimeoutSeconds int `json:"timeout_seconds"`

	// OpenSubtitlesAPIKey enables the subtitle search/download feature. Get a
	// free one at https://www.opensubtitles.com/en/consumers — without it the
	// API refuses every request.
	OpenSubtitlesAPIKey string `json:"opensubtitles_api_key"`

	// Delfan hosts. Both rotate, so these are overridable; empty falls back to
	// the current defaults baked into the delfan client.
	DelfanLoginHost string `json:"delfan_login_host"`
	DelfanAPIHost   string `json:"delfan_api_host"`
}

// Default returns the working settings for the known API.
func Default() Config {
	return Config{
		Hosts: []string{
			"http://cdntest.host4dns.n2bapp.ir",
			"http://mjapiservers.com",
		},
		BasePath:       "/playstore/api",
		APISecretKey:   "1661e8b60126d9f9",
		Version:        "4.5.0",
		Country:        "other",
		SP:             true,
		TimeoutSeconds: 30,
	}
}

// Validate reports the first problem that would stop the client from working.
func (c Config) Validate() error {
	if len(c.CleanHosts()) == 0 {
		return errors.New("no hosts configured — add at least one in Settings")
	}
	for _, host := range c.CleanHosts() {
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			return errors.New("host " + host + " must start with http:// or https://")
		}
	}
	return nil
}

// CleanHosts returns the configured hosts with blanks and trailing slashes
// removed.
func (c Config) CleanHosts() []string {
	var hosts []string
	for _, host := range c.Hosts {
		host = strings.TrimRight(strings.TrimSpace(host), "/")
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// Path is the on-disk location of the config file.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "MovieFinder", "config.json"), nil
}

// Load reads the stored config, falling back to Default on first run.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}

	// Start from the defaults so fields added in later versions keep a sane
	// value when an older config file is read.
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Default(), err
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.BasePath == "" {
		cfg.BasePath = Default().BasePath
	}
	if len(cfg.CleanHosts()) == 0 {
		cfg.Hosts = Default().Hosts
	}
	return cfg, nil
}

// Save writes the config, creating the directory if needed.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

package delfan

import "testing"

// Empty options must reproduce exactly the URLs the app used before any of this
// was configurable, so an untouched install keeps working.
func TestDefaultOptionsBuildTheOriginalURLs(t *testing.T) {
	c := NewWithOptions(Options{})

	wantLogin := DefaultLoginHost + "/app-plus/users.php?key=" + DefaultAPIKey + "&action=login"
	if got := c.loginURL(); got != wantLogin {
		t.Errorf("loginURL()  = %q\nwant          %q", got, wantLogin)
	}

	wantAction := DefaultAPIHost + "/app-plus/vp1.php?key=" + DefaultAPIKey + "&action=vitrin"
	if got := c.actionURL("vitrin"); got != wantAction {
		t.Errorf("actionURL() = %q\nwant          %q", got, wantAction)
	}
}

func TestOptionsOverrideEveryPartOfTheURL(t *testing.T) {
	c := NewWithOptions(Options{
		LoginHost:     "http://login.example",
		APIHost:       "http://api.example",
		BasePath:      "/v2",
		LoginEndpoint: "auth.php",
		APIEndpoint:   "gate.php",
		APIKey:        "KEY123",
		AppVersion:    "9.9.9",
	})

	if got, want := c.loginURL(), "http://login.example/v2/auth.php?key=KEY123&action=login"; got != want {
		t.Errorf("loginURL()  = %q, want %q", got, want)
	}
	if got, want := c.actionURL("search"), "http://api.example/v2/gate.php?key=KEY123&action=search"; got != want {
		t.Errorf("actionURL() = %q, want %q", got, want)
	}
	if c.opts.AppVersion != "9.9.9" {
		t.Errorf("AppVersion = %q, want it carried through to the signed body", c.opts.AppVersion)
	}
}

// Whatever slashes the user types, the URL must come out with exactly one
// between each part — this is the field most likely to be pasted awkwardly.
func TestBasePathSlashesAreNormalised(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/app-plus", "/app-plus/users.php"},
		{"app-plus", "/app-plus/users.php"},
		{"/app-plus/", "/app-plus/users.php"},
		{"//app-plus//", "/app-plus/users.php"},
		{"  /app-plus  ", "/app-plus/users.php"},
		{"/a/b", "/a/b/users.php"},
		{"/", "/users.php"}, // a server with no prefix at all
	}
	for _, tc := range cases {
		c := NewWithOptions(Options{LoginHost: "http://h", BasePath: tc.in})
		want := "http://h" + tc.want + "?key=" + DefaultAPIKey + "&action=login"
		if got := c.loginURL(); got != want {
			t.Errorf("BasePath %q -> %q, want %q", tc.in, got, want)
		}
	}
}

func TestHostTrailingSlashIsDropped(t *testing.T) {
	c := NewWithOptions(Options{LoginHost: "http://h/", APIHost: "http://a///"})
	if got, want := c.loginURL(), "http://h/app-plus/users.php?key="+DefaultAPIKey+"&action=login"; got != want {
		t.Errorf("loginURL() = %q, want %q", got, want)
	}
	if got, want := c.actionURL("x"), "http://a/app-plus/vp1.php?key="+DefaultAPIKey+"&action=x"; got != want {
		t.Errorf("actionURL() = %q, want %q", got, want)
	}
}

// A blank field means "use the default", not "use an empty string" — otherwise
// clearing one box in Settings would silently build a broken URL.
func TestBlankFieldsFallBackToDefaults(t *testing.T) {
	c := NewWithOptions(Options{
		LoginHost: "   ", APIHost: "", BasePath: "  ",
		LoginEndpoint: "", APIEndpoint: "   ", APIKey: "", AppVersion: "  ",
	})
	if c.opts.LoginHost != DefaultLoginHost {
		t.Errorf("LoginHost = %q, want the default", c.opts.LoginHost)
	}
	if c.opts.APIEndpoint != DefaultAPIEndpoint {
		t.Errorf("APIEndpoint = %q, want the default", c.opts.APIEndpoint)
	}
	if c.opts.APIKey != DefaultAPIKey {
		t.Errorf("APIKey = %q, want the default", c.opts.APIKey)
	}
	if c.opts.AppVersion != DefaultAppVersion {
		t.Errorf("AppVersion = %q, want the default", c.opts.AppVersion)
	}
}

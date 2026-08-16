# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Windows desktop client (Go + [Fyne](https://fyne.io)) for browsing and searching the `playstore` movie API, plus subtitle search/download via OpenSubtitles. Ships as a single self-contained `.exe`.

## Build and test

There is **no Go toolchain on this machine**. Everything goes through Docker. Do not suggest `go build`/`go test` on the host.

```powershell
.\build.ps1                  # -> dist\MovieFinder.exe, and refreshes go.sum
```

Equivalent to `docker build --target export --output "type=local,dest=dist" .`, plus moving the generated `go.sum` from `dist\` back to the repo root.

Tests and vet run in the build image, which already carries the CA and module cache:

```powershell
docker build --target base -t moviefinder-base .
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux moviefinder-base `
    go test ./internal/api/... ./internal/config/... ./internal/opensubtitles/... ./internal/mysubs/... ./internal/delfan/... ./internal/player/... ./internal/stream/... ./internal/download/... ./internal/safe/...
```

The Dockerfile is a shared `base` stage plus `build-windows` / `build-linux` targets (with `export` / `export-linux` scratch stages). `base` now carries the Linux GL/X11 dev libs too, so it can build both OSes. See BUILD.md for the user-facing build steps.

For iterating on `internal/ui` (its tests want a display, so vet/compile is the fast check), mount a cache volume and build directly instead of the full `docker build`:

```powershell
docker volume create moviefinder-gocache
docker run --rm -v "${PWD}:/src" -v moviefinder-gocache:/root/.cache/go-build -w /src `
    -e GOOS=windows -e GOARCH=amd64 -e CGO_ENABLED=1 -e CC=x86_64-w64-mingw32-gcc -e CXX=x86_64-w64-mingw32-g++ `
    moviefinder-base go build ./...
```

This turns a UI-only edit-compile cycle from ~5 minutes into seconds. A stray unused import is a hard compile error in Go — this is the fast way to catch that before running the full export build. (The `moviefinder-build` image from earlier still works if you already have it; new checkouts should tag `base`.)

Add `-run TestName` for a single test. **Exclude `./internal/ui`** from headless test runs — it compiles now that `base` carries the GL/X11 libs, but its tests would need a display. Compile it via the full `build-windows`/`build-linux` targets or the fast cache build above.

## Environment constraints

These bit during initial setup and are already handled. Don't undo them:

- **A local proxy (Fiddler) re-signs HTTPS.** Windows trusts `DO_NOT_TRUST_FiddlerRoot`, a fresh container does not, so module downloads fail with `x509: certificate signed by unknown authority`. `export-proxy-ca.ps1` exports the CA into `certs\`; the Dockerfile trusts everything there. Re-run it if the CA is regenerated. A plain `golang:` image will fail to fetch modules for this reason — always use the `build` target.
- **`proxy.golang.org` is geo-blocked here.** Its zips come from Google Cloud Storage, which answers `403 ... this service is not available in your location`. The build uses `GOPROXY=https://goproxy.cn,direct`, overridable via `--build-arg GOPROXY=...`.
- **The Dockerfile uses `go get ./... && go mod tidy -e`, not `go mod tidy`.** Plain `tidy` also walks the test-only dependencies of every dependency, pulling a long tail of modules the binary never links against and turning any mirror hiccup into a failed build.
- **The built-in OpenSubtitles key is a build-time secret, not a source constant.** `opensubtitles.DefaultAPIKey` is an empty `var` in source; the real key is injected by `build.ps1` from the gitignored `.env` via `-ldflags -X`. So the key lives only in `.env` (gitignored) and the compiled `.exe` (gitignored) — never in the repo. A build without `.env` just has no default key. If you add another baked-in secret, follow the same pattern: empty `var`, a `.env` line, a `--build-arg`, and an `-X` flag; do not turn it back into a `const`.
- **Keep `.ps1` files ASCII-only.** Windows PowerShell 5.1 reads scripts as ANSI without a BOM; a UTF-8 em dash decodes into a smart quote that PowerShell treats as a string delimiter, breaking the parse in a place far from the real character.
- `-H windowsgui` in the link flags suppresses the console window behind the GUI. Drop it temporarily if you need to see stdout while debugging.
- **The committed `go.mod`/`go.sum` must be the full ones, not the minimal file with just the direct `fyne.io/fyne/v2` require line.** The Dockerfile runs `go get ./...` inside the image to fill in the ~30 indirect requirements; both files are exported back out (`export` stage `COPY`s them, `build.ps1` moves them to the repo root) so the project can be built without Docker resolving dependencies from scratch each time. If a build ever fails outside Docker with `go: updates to go.mod needed`, this is why — it means the exported file was reverted or never landed.

## Architecture

```
cmd/moviefinder  -> internal/ui  -> internal/api            -> internal/config
                               |-> internal/delfan
                               |-> internal/opensubtitles
                               \-> internal/mysubs
```

`internal/opensubtitles`, `internal/mysubs`, `internal/delfan` and `internal/player` are independent of `internal/api`. The UI switches between two content sources (`sourceMovieFinder`, `sourceDelfan`); `delfan_adapt.go` converts Delfan's types onto `api.Movie`/`api.Detail` so the poster grid and detail pane render both without knowing which source they came from. `fetch`/`fetchDetail` in `app.go` are the two branch points.

### Playback

There is no in-app video decoder — Fyne has no media support, so `internal/player` drives an external player instead (the same delegation the Delfan app itself uses). `player.Detect` probes a configured path, then PATH, then known install locations for PotPlayer / mpv / VLC / MPC-HC, and `Play` appends the subtitle with that player's own flag (`/sub=`, `--sub-file=`, `/sub`). The Play flow (`ui/playback.go`) resolves nothing extra — link URLs are already playable (Delfan links are resolved at detail-load, playstore links are direct) — it just downloads the chosen OpenSubtitles `.srt` to a temp file and hands both to the player.

### Download while playing (`internal/stream`)

The "one connection" constraint drives the whole design. `stream.Server` opens exactly ONE upstream GET, writes the body sequentially into the save file, and runs a `127.0.0.1` HTTP server that serves the player from that growing file. Only the upstream download touches the internet; the player reads from localhost, so a player that opens several connections still costs one internet connection. `TestStreamSavesFullFileFromOneConnection` asserts the upstream is hit exactly once.

Range requests are served from the save file, and a range that seeks past the current download point **blocks on the sync.Cond until those bytes arrive** rather than opening a second upstream connection (`TestRangeAheadOfDownloadBlocksThenServes`). The consequence, worth remembering: an `.mkv` whose seek index (Cues) is at the end makes a player request the tail first, which stalls playback until the download reaches it — inherent to single-connection sequential download, not a bug. The download keeps running after the player disconnects (so the saved file completes); `Stop` cancels it and is called when a new playback replaces the old one. Reader visibility relies on `os.File.Write` being visible to a separate `os.Open`+`ReadAt` handle without `fsync` — true within one process on Windows, so no expensive per-chunk sync.

### The API and its two quirks

Base path `/playstore/api`; `api_secret_key`, `version`, `country` and `sp` go on every request (`Client.commonParams`). Endpoints in use: `get_movies?page=`, `search?q=`, `get_single_details?type=&id=`, `get_movie_by_genre_id?id=&page=`, `get_slider`.

Two behaviours shape the decoding layer — don't "simplify" them away:

- **Every scalar is a JSON string**, including numbers and the `is_tvseries`/`enable_download` flags. The structs in `model.go` are typed as `string` deliberately; `Movie.IsSeries()`/`Kind()`/`Year()` do the interpreting.
- **Errors arrive with HTTP 200** as `{"status":"error","message":"…"}`. The status code alone never reveals a failure, so every decode path runs `checkAPIError` on the body first.

`search` returns `{movie:[], tvseries:[], tv_channels:[]}` rather than a flat list, and is **not paged** — `Client.Search` flattens the three categories, and the UI disables the pager while a search is active. The listing endpoints return a bare array.

Field names that lie, confirmed against live responses:

- `Movie.writer` holds the **localized title**, not a writer credit.
- `DownloadLink.resolution` is a decorative `⇩` glyph on every row; the real quality is in `label` (`"720P زیرنویس*"`, `"4K X265 زیرنویس**"`). `Describe()` keeps `resolution` only when it contains a digit, so a sibling deployment that fills it properly still works.
- `DownloadLink.file_size` is a bare number of **megabytes**, frequently `null`.

**Download URLs are signed and expire.** They point at separate `dl*.downlaodhaa.net` hosts and carry `md5` + `expires` parameters, so they must not be cached or persisted — re-fetch the details instead. The app only displays them (`ui.showLinks`); it deliberately does not fetch the movie files itself. This is a product decision, not a limitation — don't reintroduce a download path for `DownloadLink` without checking that's actually wanted.

**Image URLs in detail responses point at a dead CDN.** `cdn.p1kp9726i2hf0yu21upnpio3bls6.cf` no longer resolves, while the identical `/playstore/uploads/...` path is served by the API hosts. `Client.Image` therefore retries a failed fetch against `ActiveHost()`. Listing endpoints return working URLs already, so this only bites on the detail pane.

### Host failover

`config.Hosts` is an ordered mirror list. `Client.get` starts at `c.active` — the host that answered last — and walks the list from there, pinning whichever succeeds. That pinning is the point: a dead mirror costs one failed request, not one per call. `TestFailsOverToNextHostAndPinsIt` guards it.

`Client.fetch` returns a `tryNext bool` alongside its error, which decides whether the failure is the host's fault. Transport errors and `5xx` are retryable; `4xx` is not, because it would fail identically on every mirror. Adding a new failure case means deciding which side it falls on.

Config lives at `%APPDATA%\MovieFinder\config.json`. `config.Load` starts from `Default()` and unmarshals over it, so fields added later keep a sane value when an older file is read.

### The Delfan source and its request signing

`internal/delfan` talks to a second movie API whose every gated request carries two client-computed fields, reverse-engineered from the app. Do not "clean up" the constants or the call ordering — they are load-bearing:

- **`body`** — built once per session by `buildBody`: `MD5(rand+"cotation") + auth + "fdaa94a151e2c5d4" + "74a290e8" + auth + "y87mdjsodon" + appversion + MD5(date+"cotation")`. The two MD5 halves are padding the server cannot verify; the real payload is the login `auth` token embedded twice.
- **`an`** — a rolling anti-replay nonce, `MD5(q1 + q2 + 101)`. `q1`/`q2` are integers the server returns in **every** response, and each gated request must carry the nonce derived from the **previous** response's q1/q2. `roundtrip` threads this state: sign with the current q1/q2, send, then advance from the reply. This is why requests serialize on `c.mu` — two concurrent calls would reuse a nonce and one would be rejected.

The nonce is **per-host**: login is on one host, the gated endpoints on another, so `ensureSession` makes a `vitrin` call (which does not validate the nonce) on the gated host purely to seed its q1/q2 before the first real request. Getting this ordering wrong yields the server's generic `"N - error identifying connection"` — where N advancing (1→2) means you cleared one gate and hit the next.

Both hosts rotate; the app rediscovers them from fragmented fields split across a response (e.g. `protocol`+`dtile`+`sim`+`chart` reassemble into the API host). The client uses hardcoded defaults overridable via config rather than reimplementing that discovery. Download links are `play.php` URLs that 302-redirect to the real signed file; `ResolveDownloadURL` reads the `Location` header rather than following it (`CheckRedirect` returns `ErrUseLastResponse`).

`TestSearchThreadsTheRollingNonce` and `TestStaleNonceIsRejected` guard the chain; the opt-in `DELFAN_LIVE=1` tests exercise it against the real servers.

### Why the hosts default to `http://`

Both configured domains resolve to the same IP and serve the API fine over HTTP, but over HTTPS they present a certificate issued for a different name, so verification always fails. `InsecureTLS` exists for anyone who switches to `https://`, but it only buys encryption without authentication — do not present it as a security improvement. Fixing the server certificate is the only real answer.

### UI threading

Fyne is not thread-safe. Every widget mutation from a goroutine **must** be wrapped in `fyne.Do(func(){ … })` — see the loader in `ui.reload` and the download progress callback. Missing this produces sporadic corruption rather than a clean failure.

`UI.mu` guards `movies`, `detail`, `links`, `page`, `query` and `selected`, which the loader goroutines write and the widget callbacks read on the UI thread. Read them through `movieAt`/`selectedLink`/`currentPage` rather than touching the fields.

`reload` and `loadDetail` each cancel their previous in-flight request (`cancelLoad`, `cancelDetail`) and drop the response if `ctx.Err() != nil`, so fast typing or fast row-clicking cannot let a stale response win a race.

### Staying up when a third-party site is down

The app talks to several services that are not the movie servers: OpenSubtitles, my-subs.co, and IMDb (a link only — the app never requests anything from IMDb). None of them may be able to stop browsing, downloading or playback, and none of them may close the window.

Two rules carry that:

- **A failure is an error, shown where it happened.** Subtitle searches write to the dialog's own status line (`sourceFailureMessage` / `playSourceFailureMessage`, which name the source and point at the alternative). Listing and detail failures go to the status bar. The Play dialog always keeps **Play without subtitle** live, whatever the subtitle sources are doing.
- **A panic anywhere must not end the process.** `internal/safe` recovers, logs the stack, and hands a short error to a reporter. In the UI, `u.bg` and `u.onUI` (in `ui/safety.go`) replace bare `go func` and `fyne.Do` — a `fyne.Do` callback runs on the main loop, so an unguarded panic there is just as fatal as one on a goroutine. Outside the UI, the download worker (`download.Queue.run`), the stream download goroutine and Delfan's link resolution are guarded the same way. **Add new background work through those helpers, not with a bare `go`.**

Why a backstop at all, when the clients return errors: every one of these goroutines is running a parser over something a remote server sent. A rotated API answering with a different shape, or a scraped page restyled overnight, produces an index-out-of-range rather than an error — and it would land in the middle of somebody's download. `TestPanicInOneTransferFailsThatJobAndNotTheQueue` pins the queue's half: the job is marked failed, the worker survives, the next job runs.

This is a backstop, not a substitute for handling failures you can see coming. Keep returning errors from the client packages.

### The poster grid

The listing is a `widget.GridWrap` of `posterCard`, which is a real widget (`ExtendBaseWidget` + `CreateRenderer`) precisely so the update callback can type-assert the item back rather than keeping a side table keyed by `CanvasObject`.

**GridWrap recycles tiles.** `posterCard.want` records the URL the tile currently expects, and the async image callback drops its result if `want` has changed since. Removing that check makes fast scrolling paint posters onto the wrong titles — intermittently, so it will not show up in a quick manual test.

`imageCache` keeps decoded posters in memory and clears wholesale past 400 entries; posters are cheap to refetch, so there is no LRU.

### Client details worth knowing

- `Client.fetch`/`Client.fetchImage` cap reads with `io.LimitReader` so a misdirected URL cannot exhaust memory.
- `ui.safeSubtitleName` (in `subtitles.go`) strips characters Windows rejects and takes `filepath.Base`, so an API-supplied filename cannot break the save dialog's pre-filled name. Keep that on any new save path — it's the same shape of guard the movie side used to need before movie downloads were removed.

### OpenSubtitles

`internal/opensubtitles` needs a free API key — confirmed live against both OpenSubtitles' current REST API and the legacy XML-RPC one VLC traditionally used: neither works without one. The REST API answers `{"message":"You cannot consume this service"}` with no key; the XML-RPC path's old public test user-agent (`OSTestUserAgent`) now answers `415 Disabled user agent`. There is no way around this — don't try a different endpoint or a hardcoded UA to route around it.

`Client.Search` tries the title's IMDb id first (`imdb_id` param, stripped of the `tt` prefix by `ui.imdbNumeric`) and only falls back to `query`+`year` if that comes back with zero results — the two lookups are indexed independently on OpenSubtitles' side, so one being empty doesn't mean the other will be.

`currentBaseURL` is a package-level `var`, not a `const`, purely so tests can redirect it at an `httptest` server — there's no runtime reason to change it. `flexInt` exists because `feature_details.year` is documented as a JSON number but this API has been known to drift, and it can't be verified live without an account; it accepts either a number or a string rather than failing the whole decode.

There is no login flow — downloads are anonymous and quota-limited per IP by OpenSubtitles. The limit is **5 downloads/day**, confirmed live: the `/download` response carries `requests` and `remaining` counters plus a `reset_time`, and the quota rolls over at 00:00 UTC. Searching is not metered, only downloading. That ceiling is why `internal/mysubs` exists; logging in raises the allowance but does not remove the cap, which is why adding a login still is not worth the credential-storage surface.

### MySubs (my-subs.co), the second subtitle source

`internal/mysubs` is a scraper, not an API client — my-subs.co publishes no API and needs no key or account, which is the whole point: it has no per-user download quota. The UI picks between the two sources per search (`ui/subprovider.go`), defaulting to `config.SubtitleProvider`.

Three page shapes carry everything:

```
/search.php?key=…                                     two panels: Tv Shows, Movies
/film-versions-{id}-{slug}-subtitles                  a movie's subtitles, all languages
/versions-{id}-{episode}-{season}-{slug}-subtitles    one episode's subtitles
```

Things that will bite whoever touches this next:

- **The episode URL is episode-then-season, not season-then-episode.** Read off the site's own dropdowns: season 2 episode 3 is `/versions-{id}-3-2-…`. The wrong order still returns a valid page for a different episode, so it fails silently. `TestEpisodeURLPutsEpisodeBeforeSeason` guards it.
- **The two page shapes need two parsers.** Movie pages list one anchor per subtitle (language in the flag's `title`, release in `<strong>`, count in `<b>`); episode pages group rows under a `<div class='version'>` heading and use single-quoted attributes. `parseSubtitlePage` tries the movie shape and falls back to the episode one.
- **`/downloads/{token}` is a gate page, not the file.** It holds a 10s JavaScript countdown and the real path in a `REAL_URL` variable. The countdown is client-side only, but the `PHPSESSID` cookie the gate sets is not — the file URL answers `302` without it. Hence the client's `cookiejar` and the two-request `Download`. `TestDownloadPassesTheGateAndKeepsTheSession` covers both halves.
- **Cloudflare fronts the site**, so requests carry a browser `User-Agent`; a blank or obviously scripted one gets a challenge page instead of HTML.
- **Language names are inconsistent** on one and the same page: `english`, `Persian`, `Farsi/Persian`, `Portuguese (Brazilian)`. `languageAliases` maps the picker's ISO codes onto case-insensitive substrings rather than exact names.
- **Matching is by title and year only** — the site exposes no IMDb id anywhere. `pickTitle` scores hits because the site's own order is alphabetical, so "Breaking Bad" lists "Breaking Bad Minisodes" first. A series hit with no season/episode given returns an error asking for them rather than guessing S01E01.
- Some uploads are zipped; `Download` unwraps the first subtitle inside so the player always gets a subtitle file.

The parsing is regex-based on purpose: the markup is machine-generated and simple, and an HTML-parser dependency would be one more module for the Docker build to resolve. The opt-in `MYSUBS_LIVE=1` tests are the real check — a restyle breaks a scraper silently, and only the live pages will tell you.

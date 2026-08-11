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
docker build --target build -t moviefinder-build .
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux moviefinder-build `
    go test ./internal/api/... ./internal/config/... ./internal/opensubtitles/...
```

For iterating on `internal/ui` itself — which only compiles under the Windows cross-build, so vet/test can't touch it — mount a cache volume and build directly instead of going through the full `docker build` (which reruns dependency resolution and takes minutes):

```powershell
docker volume create moviefinder-gocache
docker run --rm -v "${PWD}:/src" -v moviefinder-gocache:/root/.cache/go-build -w /src `
    -e GOOS=windows -e GOARCH=amd64 -e CGO_ENABLED=1 -e CC=x86_64-w64-mingw32-gcc -e CXX=x86_64-w64-mingw32-g++ `
    moviefinder-build go build ./...
```

This turns a UI-only edit-compile cycle from ~5 minutes into seconds. A stray unused import is a hard compile error in Go — this is the fast way to catch that before running the full export build.

Add `-run TestName` for a single test. **Exclude `./internal/ui`** from Linux test runs — Fyne needs X11/GL headers that are not installed in the image; the UI package only compiles under the Windows cross-build.

## Environment constraints

These bit during initial setup and are already handled. Don't undo them:

- **A local proxy (Fiddler) re-signs HTTPS.** Windows trusts `DO_NOT_TRUST_FiddlerRoot`, a fresh container does not, so module downloads fail with `x509: certificate signed by unknown authority`. `export-proxy-ca.ps1` exports the CA into `certs\`; the Dockerfile trusts everything there. Re-run it if the CA is regenerated. A plain `golang:` image will fail to fetch modules for this reason — always use the `build` target.
- **`proxy.golang.org` is geo-blocked here.** Its zips come from Google Cloud Storage, which answers `403 ... this service is not available in your location`. The build uses `GOPROXY=https://goproxy.cn,direct`, overridable via `--build-arg GOPROXY=...`.
- **The Dockerfile uses `go get ./... && go mod tidy -e`, not `go mod tidy`.** Plain `tidy` also walks the test-only dependencies of every dependency, pulling a long tail of modules the binary never links against and turning any mirror hiccup into a failed build.
- **Keep `.ps1` files ASCII-only.** Windows PowerShell 5.1 reads scripts as ANSI without a BOM; a UTF-8 em dash decodes into a smart quote that PowerShell treats as a string delimiter, breaking the parse in a place far from the real character.
- `-H windowsgui` in the link flags suppresses the console window behind the GUI. Drop it temporarily if you need to see stdout while debugging.
- **The committed `go.mod`/`go.sum` must be the full ones, not the minimal file with just the direct `fyne.io/fyne/v2` require line.** The Dockerfile runs `go get ./...` inside the image to fill in the ~30 indirect requirements; both files are exported back out (`export` stage `COPY`s them, `build.ps1` moves them to the repo root) so the project can be built without Docker resolving dependencies from scratch each time. If a build ever fails outside Docker with `go: updates to go.mod needed`, this is why — it means the exported file was reverted or never landed.

## Architecture

```
cmd/moviefinder  -> internal/ui  -> internal/api            -> internal/config
                               \-> internal/opensubtitles
```

`internal/opensubtitles` is independent of `internal/api` — it only needs a title, IMDb id and year, which `internal/ui` extracts from an `api.Detail` before calling it.

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

### Why the hosts default to `http://`

Both configured domains resolve to the same IP and serve the API fine over HTTP, but over HTTPS they present a certificate issued for a different name, so verification always fails. `InsecureTLS` exists for anyone who switches to `https://`, but it only buys encryption without authentication — do not present it as a security improvement. Fixing the server certificate is the only real answer.

### UI threading

Fyne is not thread-safe. Every widget mutation from a goroutine **must** be wrapped in `fyne.Do(func(){ … })` — see the loader in `ui.reload` and the download progress callback. Missing this produces sporadic corruption rather than a clean failure.

`UI.mu` guards `movies`, `detail`, `links`, `page`, `query` and `selected`, which the loader goroutines write and the widget callbacks read on the UI thread. Read them through `movieAt`/`selectedLink`/`currentPage` rather than touching the fields.

`reload` and `loadDetail` each cancel their previous in-flight request (`cancelLoad`, `cancelDetail`) and drop the response if `ctx.Err() != nil`, so fast typing or fast row-clicking cannot let a stale response win a race.

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

There is no login flow — downloads are anonymous and quota-limited per IP by OpenSubtitles (typically a handful/day). Add one only if a user actually hits that limit; it's not worth the added credential-storage surface otherwise.

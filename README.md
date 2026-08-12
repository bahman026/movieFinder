# MovieFinder

A Windows desktop client (Go + [Fyne](https://fyne.io)) for browsing, searching and downloading from the `playstore` movie API. Built inside Docker, so no Go toolchain is needed on the machine.

## Build

```powershell
.\build.ps1
```

or directly:

```powershell
docker build --target export --output "type=local,dest=dist" .
```

The result is a single self-contained `dist\MovieFinder.exe` — no runtime, no DLLs, no installer.

The first build downloads the Go image and the mingw cross-compiler (a few minutes). Later builds reuse the layer cache. The build also emits `dist\go.sum`; `build.ps1` moves it into the repo root so dependency versions stay pinned from then on.

### Build secrets (`.env`)

The built-in OpenSubtitles key is **not** stored in source. Copy `.env.example` to `.env` and fill it in:

```
OPENSUBTITLES_API_KEY=your_key_here
```

`build.ps1` reads `.env` and injects each value into the binary at link time (`-ldflags -X`). `.env` is gitignored, so the key lives only in that local file and the compiled `.exe` (also gitignored) — never in the repository. Building without a `.env` just produces a binary with no built-in key, and users then supply their own under Settings.

### Tests

```powershell
docker build --target build -t moviefinder-build .
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux moviefinder-build `
    go test ./internal/api/... ./internal/config/...
```

`./internal/ui` is excluded — Fyne needs X11/GL headers that the build image does not carry; that package only compiles under the Windows cross-build.

### Two things this machine needed

Both are already handled in the Dockerfile, but worth knowing if you build elsewhere:

1. **Fiddler was intercepting the container's HTTPS.** Windows trusts `DO_NOT_TRUST_FiddlerRoot`, a fresh Linux container does not, so module downloads failed with `x509: certificate signed by unknown authority`. `.\export-proxy-ca.ps1` pulls that CA out of the Windows store into `certs\`, and the build trusts whatever is in there. The certificate is trusted **only inside the build image** — the shipped `.exe` uses the Windows certificate store.

2. **`proxy.golang.org` is geo-blocked from this network.** Its module zips come from Google Cloud Storage, which answers `403 ... this service is not available in your location`. The build therefore uses `GOPROXY=https://goproxy.cn,direct`. Override it with `--build-arg GOPROXY=...` if you have a better mirror.

## What it does

- **Two sources**, switchable from the dropdown in the toolbar: **MovieFinder** (the `playstore` API) and **Delfan** (a second, separate movie API)
- **Browse** the main listing as a poster grid
- **Search** by title
- **Details** for the selected title — rating, genre, description, and its download links
- **Show links** for the title, each with its full URL and a copy button
- **Find Subtitles** — search and download from OpenSubtitles for the selected title, on either source
- **Automatic host failover** between mirrors (MovieFinder source)

### The Delfan source

A second movie catalogue on a separate set of servers, browsed and searched the same way as the primary source — pick **Delfan** in the toolbar dropdown. Browse shows the app's home page (newly added movies and series); search returns matches; the detail pane lists each quality's download link (which resolves to the real, signed file when opened). **Find Subtitles** works here too, and searches OpenSubtitles by the film's English title, which the client parses out of the Persian description.

Its API signs every request, so the client logs in, seeds a rolling per-request nonce, and threads that nonce through each call automatically — see `internal/delfan` and the architecture notes in CLAUDE.md. The two hosts rotate occasionally; if the source stops working, override them under `Settings → Delfan API host`.

The app does not download the movie files themselves; it surfaces the links for you to hand to whatever downloader you prefer. Subtitles are the one thing it does download directly, since they're small and the whole point is to save one next to the video.

### Subtitles

**Find Subtitles** requires your own free OpenSubtitles API key — register an account and create a "consumer" (API application) at <https://www.opensubtitles.com/en/consumers>, then paste the key into `Settings → OpenSubtitles key`. Without it the API refuses every request with "You cannot consume this service"; this was confirmed against the live service rather than assumed, and it holds for both OpenSubtitles' current REST API and the legacy XML-RPC one VLC traditionally used — the old public test user-agent that used to allow anonymous access has since been disabled.

Searching prefers the title's IMDb id, which OpenSubtitles matches far more precisely than free text, and falls back to title + year only if that comes back empty. Results show the movie name, release filename (the thing you actually match against your video file), upload date, download count and rating, filtered to a language you pick — English by default. Downloading a subtitle opens the OS's native save dialog rather than a fixed folder, since the video it belongs with could be anywhere.

Downloads made without logging in are quota-limited per IP by OpenSubtitles, typically a handful per day; there's no login flow here, since the anonymous quota is enough for occasional use.

### Posters

Each tile shows the poster with the IMDb rating badge bottom-left and the release year bottom-right, and the title underneath.

Posters load lazily as tiles scroll into view and are cached in memory, so paging back is instant. Two details worth knowing:

- The grid recycles tiles while scrolling, so each tile records which poster it is waiting for and discards a late-arriving image if it has since been reused for a different title. Without that, fast scrolling paints posters onto the wrong cards.
- Detail responses return image URLs on a CDN host that no longer resolves, while the same path is served by the API host. A failed image fetch is therefore retried against the host that is currently answering.

## Host failover

`Settings → Hosts` holds one host per line, tried top to bottom:

```
http://cdntest.host4dns.n2bapp.ir
http://mjapiservers.com
```

The client keeps using whichever host answered last and only moves down the list when that one stops responding, so a dead mirror costs one failed request rather than one per call. The status bar shows which server is currently serving you.

Failover triggers on connection refused, timeout, DNS failure, TLS failure and `5xx`. It deliberately does **not** trigger on `4xx` or on an API-level error, since those would come back identically from every mirror — the error is reported instead of silently retried.

**Test each host** in Settings probes every host individually rather than through the failover path, so a working mirror cannot hide a broken one.

### Why the hosts are `http://`

Both domains resolve to the same IP and both serve the API fine over HTTP. Over HTTPS they present a certificate issued for a different name, so verification fails (`SEC_E_WRONG_PRINCIPAL` / `x509: certificate is valid for ...`).

There is a `Skip TLS certificate verification` toggle for `https://` hosts, but be clear about what it buys: with the wrong certificate, HTTPS gives you encryption without authentication — you cannot tell the real server from anyone able to intercept the connection. Neither setting gives you an authenticated channel to these servers. Fixing the certificate on the server is the only real answer; until then HTTP is at least honest about it.

## API notes

Base path `/playstore/api`, with `api_secret_key`, `version`, `country` and `sp` sent on every request.

| Endpoint | Parameters | Returns |
| --- | --- | --- |
| `get_movies` | `page` | bare array of titles |
| `search` | `q` | `{movie:[], tvseries:[], tv_channels:[]}` |
| `get_single_details` | `type` (`movie`/`tvseries`), `id` | one title with `download_links[]`, `videos[]`, `genre[]`, `cast[]`, … |
| `get_movie_by_genre_id` | `id`, `page` | bare array of titles |
| `get_slider` | — | `{slider_type, data:[]}` |

Quirks the client works around:

- **Every scalar is a JSON string**, including numbers and the `is_tvseries` / `enable_download` flags.
- **Errors arrive with HTTP 200**, as `{"status":"error","message":"…"}`. The status code alone never reveals a failure, so every response body is checked for that envelope.
- **`search` is not paged** — it returns all matches at once — so the pager is disabled while a search is active.
- **Some field names are misleading.** `writer` on a listing entry is the localized title. `resolution` on a download link is a decorative `⇩` glyph, with the real quality in `label`. `file_size` is a bare number of megabytes and is often `null`.
- **Links expire.** They point at separate `dl*.downlaodhaa.net` file hosts and are signed with `md5` + `expires`, so the app fetches them fresh with the title's details rather than storing them. If a link has gone stale, reselect the title to get a fresh one.

The `Links (n)` list in the detail pane comes straight from `download_links`.

## Layout

```
cmd/moviefinder/main.go            entry point
internal/api/client.go             endpoints, host failover, image fetch
internal/api/model.go              Movie / Detail types, error-envelope detection
internal/api/client_test.go        failover and decoding tests
internal/config/config.go          settings load/save (%APPDATA%)
internal/delfan/client.go          Delfan signed API: login, rolling nonce, search, details
internal/opensubtitles/client.go   OpenSubtitles search and download
internal/ui/app.go                 window, poster grid, detail pane, links
internal/ui/poster.go              poster grid tiles and image cache
internal/ui/subtitles.go           subtitle search dialog and download
internal/ui/settings.go            settings dialog and per-host connection test
Dockerfile                         mingw cross-compile to a Windows .exe
build.ps1                          one-command build wrapper
export-proxy-ca.ps1                exports the local TLS-interception CA for the build
```

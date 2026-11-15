# MovieFinder Web — TODO

A small Laravel site that does the same Delfan anonymous login and request
signing as `internal/delfan` in the Go desktop app, and shows the results as web pages.

## Done

- [x] Laravel 13 scaffold in `web/` (no database needed: sessions and cache locks are stored as files)
- [x] `config/delfan.php`: every endpoint value can be overridden with `DELFAN_*` env vars
- [x] `App\Services\Delfan\DelfanClient`, a port of the Go client
  - [x] anonymous login and auth token
  - [x] per-session `body` (MD5 padding + auth + constants + app version)
  - [x] rolling nonce `an = MD5(q1 + q2 + 101)`, advanced after every reply
  - [x] `vitrin` seed call on the gated host before the first real request
  - [x] requests in one session are serialized with a cache lock (the PHP version of the Go mutex)
  - [x] `home`, `search` (paged), `details` (films and series), `resolve` (reads the play.php 302 `Location` without following it)
- [x] Privacy
  - [x] random `android_id` for each session (the desktop app's fixed one is not used)
  - [x] generic User-Agent picked at random for each session
  - [x] upstream auth and nonce kept only in the visitor's encrypted session; never logged or stored in a DB
  - [x] "New session" button drops the upstream session and regenerates the session id
  - [x] `no-referrer` on the page and on images
  - [x] `/resolve` follows only links the title page offered (index into the session, not a raw URL), so it is not an open redirect or SSRF
- [x] Pages: latest, search with pager, title detail with links / seasons
- [x] Feature test: nonce threading and random device id
- [x] Small Docker image (`Dockerfile`: composer stage, then `php:8.4-cli-alpine` runtime)
- [x] `deploy` branch that contains only `web/` (see `publish-branch.sh`)

## Next

- [ ] Retry once with a fresh login when the server answers `"N - error identifying connection"` (expired session or nonce)
- [ ] Movie/series filter options on search (`type`, `genre`, `year_from`/`year_to`, `imdb`, `sort_by`)
- [ ] Cast search and cast pages (`search_cast`, `show_movie_cast`)
- [ ] Short server-side cache of `vitrin` results, shared across visitors so the home page doesn't need a login for each one
- [ ] Image proxy (optional) so visitors' browsers don't contact the image CDN directly
- [ ] Rate limiting (`throttle` middleware) on search and resolve
- [ ] Subtitle search (OpenSubtitles needs an API key from `.env`, never in source; my-subs.co scraper)
- [ ] Persian / RTL UI language toggle
- [ ] Error page for when hosts rotate, explaining which `DELFAN_*` setting to update
- [ ] Production server: put a real web server in front, or switch to FrankenPHP if the PHP built-in server is not enough
- [ ] CI: run `php artisan test` and build the image on the `deploy` branch

## Run

```sh
# local
cd web && cp .env.example .env && php artisan key:generate && php artisan serve

# docker
docker build -t moviefinder-web web
docker run --rm -p 8080:8080 moviefinder-web     # http://localhost:8080
```

With no `APP_KEY`, the container makes a throwaway key, so all sessions are lost when it restarts.

## Deploy only this folder

```sh
web/publish-branch.sh                 # refresh the `deploy` branch from web/
git push origin deploy
# on the server:
git clone --single-branch -b deploy <repo-url> moviefinder-web
```

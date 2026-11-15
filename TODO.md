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

## Done (round 2)

- [x] Playstore source (`App\Services\Playstore\PlaystoreClient`, a port of `internal/api`)
  - [x] mirror list with the last good host remembered (shared cache); 5xx/unreachable hosts are skipped, a 4xx stops
  - [x] errors that come back as HTTP 200 `{"status":"error"}`; numbers sent as text; search split into 3 lists and not paged
  - [x] dead CDN image addresses rewritten onto the host that is answering
  - [x] latest titles with paging, featured slider, search, title page (genres, cast, IMDb link), links and seasons
- [x] Source switch in the header (MovieFinder / Delfan), remembered for the session; both use the same pages
- [x] Subtitles on every title page (source, language, season/episode for series), downloaded as a file
  - [x] OpenSubtitles (`OpenSubtitlesClient`): IMDb id first, then title + year, filtered to the right title; key only in `.env`
  - [x] my-subs.co (`MySubsClient`): title scoring, episode-before-season addresses, download gate with cookie, zip unwrapping, Cloudflare 52x retries
  - [x] when one source fails, the message points at the other
- [x] Delfan: a new login and one retry on "error identifying connection"; actor search and actor pages
- [x] Rate limits: 60/min for pages, 15/min for opening links and subtitle downloads
- [x] Error messages say which `.env` setting to update when servers have moved
- [x] Tests: mirror failover, error envelope, image rewrite, my-subs episode address and gate, OpenSubtitles title filter

## Next

- [ ] Delfan search filters (`type`, `genre`, `year_from`/`year_to`, `imdb`, `sort_by`): the accepted values are
      not known yet. Read them from the app or capture real requests before adding a form.
- [ ] Playstore genre pages: `get_movie_by_genre_id` needs a genre id, but detail responses only give genre
      names and urls. Find where the ids come from.
- [ ] Short shared cache for home and slider results
- [ ] Optional image proxy, so visitors' browsers don't contact the image hosts directly
- [ ] Persian / RTL interface language
- [ ] OpenSubtitles quota is per server IP on a website (5/day shared by all visitors). Show the remaining count,
      or default to my-subs.co (already the default).
- [ ] CI: `php artisan test` + image build on the `deploy` branch
- [ ] Production: a real web server in front, or FrankenPHP

## Run

```sh
# local
cd web && cp .env.example .env && php artisan key:generate && php artisan serve

# docker
docker build -t moviefinder-web web
docker run --rm -p 8080:8080 --env-file web/.env.production moviefinder-web   # http://localhost:8080
```

With no `APP_KEY`, the container makes a throwaway key, so all sessions are lost when it restarts.

## Deploy only this folder

```sh
web/publish-branch.sh                 # refresh the `deploy` branch from web/
git push origin deploy
# on the server:
git clone --single-branch -b deploy <repo-url> moviefinder-web
```

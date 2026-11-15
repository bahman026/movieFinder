# Deploying MovieFinder Web

The site runs as one container, `moviefinder_web`, on the shared VPS (`ssh -p 9011 deploy@87.107.104.19`),
in `~/moviefinder`. There is no database and no Redis: sessions and cache are files inside the container.
It publishes no port. The shared Caddy proxy (`togamode_edge_proxy`) reaches it as `moviefinder_web:8080` over the
external `togamode_edge` network.

The `deploy` user's shell is fish, so always send remote commands through `bash -s`.

## How a deploy happens

1. `web/publish-branch.sh && git push origin deploy` (run from the repo root on your machine).
2. GitHub Actions (`.github/workflows/deploy.yml` on the `deploy` branch) runs the tests, builds the image and
   pushes `ghcr.io/bahman026/moviefinder-web:<sha>` (and `:latest`) to GHCR. Nothing is built on the server.
3. If the repository variable `DEPLOY_ENABLED` is `true`, the workflow copies `compose.yaml` + `deploy.sh` to
   `~/moviefinder` and runs `IMAGE_TAG=<sha> ./deploy.sh`. It needs these repository secrets:
   - `DEPLOY_SSH_KEY`: a private key whose public half is in the server's `~deploy/.ssh/authorized_keys`
   - `DEPLOY_KNOWN_HOSTS`: the output of `ssh-keyscan -p 9011 87.107.104.19`

`deploy.sh` pulls the given tag, recreates only `moviefinder_web`, waits for its health check, saves the tag
for rollback, then runs `docker image prune -a -f`, which removes only images no container uses. It prints
`df -h /` before and after.

## Manual fallback (SSH from GitHub failed)

Take the commit sha from the successful `image` job, then:

```bash
ssh -p 9011 deploy@87.107.104.19 'bash -s' <<'EOF'
cd ~/moviefinder && IMAGE_TAG=<sha> ./deploy.sh
EOF
```

If `compose.yaml` or `deploy.sh` changed in that commit, copy them first:
`scp -P 9011 web/deploy/compose.yaml web/deploy/deploy.sh deploy@87.107.104.19:moviefinder/`

## Rollback

```bash
ssh -p 9011 deploy@87.107.104.19 'bash -s' <<'EOF'
cd ~/moviefinder && ./deploy.sh rollback          # the tag that ran before the last deploy
# or pick any earlier build:  IMAGE_TAG=<older sha> ./deploy.sh
EOF
```

To take the site down completely without touching anything else:
`cd ~/moviefinder && IMAGE_TAG=$(cat .current_tag) docker compose down`

## First-time setup (once)

1. **GHCR**: after the first workflow run, set the `moviefinder-web` package to Public in GitHub
   (Packages → package settings), so the server can pull it without a login. Or log the server in with a
   read-only token: `docker login ghcr.io`.
2. **Server files**:
   ```bash
   ssh -p 9011 deploy@87.107.104.19 'bash -s' <<'EOF'
   mkdir -p ~/moviefinder && chmod 700 ~/moviefinder
   EOF
   scp -P 9011 web/deploy/compose.yaml web/deploy/deploy.sh deploy@87.107.104.19:moviefinder/
   ```
   Then create `~/moviefinder/.env.production` (mode 600) from `deploy/.env.production.example`:
   `APP_URL`, a fixed `APP_KEY`, and `OPENSUBTITLES_API_KEY`.
3. **DNS**: an A record for `codegeek.ir` and `www.codegeek.ir` pointing to `87.107.104.19`. On Cloudflare it must be
   DNS only (grey cloud). It must resolve before the Caddy reload.
4. **First deploy**: `IMAGE_TAG=<sha> ./deploy.sh` (manual command above).
5. **Caddy**: append, never rewrite, at the end of `~/togamode-edge/infrastructure/production/Caddyfile`:
   ```
   # --- codegeek.ir ---
   # moviefinder (deployed in ~/moviefinder), shares this proxy because it owns 80/443.
   codegeek.ir {
         import common
         reverse_proxy moviefinder_web:8080
   }

   www.codegeek.ir {
         import common
         redir https://codegeek.ir{uri} permanent
   }
   ```
   Steps: `cp Caddyfile Caddyfile.bak-$(date +%F-%H%M)`, append, `diff` against the backup, then
   `docker exec togamode_edge_proxy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`, then
   `docker exec togamode_edge_proxy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile`.
   Never restart or recreate the proxy container.

## Verify

- `curl -I https://codegeek.ir` → 200 with a valid certificate
- `https://togamode.ir`, `https://honarkadenajar.ir`, `https://memflash.ir` still answer
- `docker ps` shows every container that was there before, all running
- `df -h /` still has a few GB free

## Limits on this server

- `mem_limit: 256m`; the image is about 75 MB compressed. Logs are capped at 3 × 10 MB.
- Never run `docker system/volume/network/container prune`, and never stop anything that isn't `moviefinder_*`.

#!/usr/bin/env bash
# Pull a given image tag and restart the moviefinder stack. Touches nothing else.
#
#   IMAGE_TAG=<git sha> ./deploy.sh        deploy that build
#   ./deploy.sh rollback                   go back to the tag that ran before the last deploy
set -euo pipefail
cd "$(dirname "$0")"
export COMPOSE_PROJECT_NAME=moviefinder

if [[ "${1:-}" == "rollback" ]]; then
    [[ -s .previous_tag ]] || { echo "no previous tag recorded" >&2; exit 1; }
    IMAGE_TAG="$(cat .previous_tag)"
fi
: "${IMAGE_TAG:?usage: IMAGE_TAG=<sha> ./deploy.sh   (or ./deploy.sh rollback)}"
[[ -f .env.production ]] || { echo ".env.production missing (needs APP_KEY, OPENSUBTITLES_API_KEY, APP_URL)" >&2; exit 1; }
export IMAGE_TAG

echo "== disk before"; df -h / | tail -1

docker compose pull web
current="$(cat .current_tag 2>/dev/null || true)"
docker compose up -d --no-deps web

echo "== waiting for health"
for _ in $(seq 1 30); do
    state="$(docker inspect -f '{{.State.Health.Status}}' moviefinder_web 2>/dev/null || echo starting)"
    [[ "$state" == healthy ]] && break
    sleep 2
done
if [[ "$state" != healthy ]]; then
    echo "moviefinder_web is $state after 60s" >&2
    docker logs --tail 40 moviefinder_web >&2 || true
    exit 1
fi

# Remember tags for rollback, only once the new one is healthy.
if [[ -n "$current" && "$current" != "$IMAGE_TAG" ]]; then echo "$current" > .previous_tag; fi
echo "$IMAGE_TAG" > .current_tag

# Removes only images that no container (of any project) uses.
docker image prune -a -f >/dev/null
echo "== disk after"; df -h / | tail -1
echo "deployed $IMAGE_TAG"

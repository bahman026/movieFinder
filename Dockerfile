# Tiny image for the MovieFinder web front-end (Laravel).
# Build:  docker build -t moviefinder-web .
# Run:    docker run --rm -p 8080:8080 moviefinder-web

# --- dependencies (composer only lives in this stage) ---
FROM composer:2 AS vendor
WORKDIR /app
COPY composer.json composer.lock ./
RUN composer install --no-dev --no-scripts --no-autoloader --no-interaction --prefer-dist
COPY . .
RUN composer dump-autoload --no-dev --optimize --classmap-authoritative

# --- runtime: PHP CLI on Alpine, built-in server, no DB, no web server ---
FROM php:8.4-cli-alpine
WORKDIR /app
COPY --from=vendor /app /app
COPY docker/entrypoint.sh /entrypoint.sh

# The app needs no database: sessions and cache locks are files.
ENV APP_ENV=production \
    APP_DEBUG=false \
    LOG_CHANNEL=stderr \
    LOG_LEVEL=warning \
    SESSION_DRIVER=file \
    SESSION_ENCRYPT=true \
    CACHE_STORE=file \
    QUEUE_CONNECTION=sync \
    DB_CONNECTION=sqlite

RUN chmod +x /entrypoint.sh \
 && rm -f bootstrap/cache/*.php \
 && mkdir -p storage/framework/sessions storage/framework/views storage/framework/cache/data storage/logs \
 && adduser -D -H app && chown -R app:app storage bootstrap/cache

USER app
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]

#!/bin/sh
set -e
# No APP_KEY passed in: make a throwaway one. Sessions (and the anonymous
# upstream login inside them) then die with the container - nothing persists.
if [ -z "$APP_KEY" ]; then
    APP_KEY="base64:$(head -c 32 /dev/urandom | base64)"
    export APP_KEY
fi
php artisan config:cache >/dev/null
php artisan route:cache >/dev/null
php artisan view:cache >/dev/null
exec php -S 0.0.0.0:8080 -t public vendor/laravel/framework/src/Illuminate/Foundation/resources/server.php

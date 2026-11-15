<?php

// Playstore API (internal/api in the Go app) and subtitle sources.
// The Delfan source has its own file, config/delfan.php.
return [
    'default_source' => env('MOVIES_DEFAULT_SOURCE', 'db1'), // db1 (playstore API) | db2 (Delfan API)

    // Serve the Watch page over plain http so the player can stream straight from
    // the database hosts (http-only): an https page would have the browser block
    // the video. Needs the http://codegeek.ir block in Caddy and no HSTS on the
    // domain; see DEPLOY.md. Off by default (local/dev stay as they are).
    'watch_over_http' => (bool) env('WATCH_OVER_HTTP', false),

    'playstore' => [
        // Ordered mirror list, comma separated. The one that answered last is tried first.
        'hosts' => env('PLAYSTORE_HOSTS', 'http://cdntest.host4dns.n2bapp.ir,http://mjapiservers.com'),
        'base_path' => '/'.trim(env('PLAYSTORE_BASE_PATH', '/playstore/api'), '/'),
        'secret_key' => env('PLAYSTORE_SECRET_KEY', '343hg4343'),
        'version' => env('PLAYSTORE_VERSION', '4.5.0'),
        'country' => env('PLAYSTORE_COUNTRY', 'other'),
        'sp' => (bool) env('PLAYSTORE_SP', true),
        'timeout' => (int) env('PLAYSTORE_TIMEOUT', 30),
    ],

    'subtitles' => [
        'default_provider' => env('SUBTITLE_PROVIDER', 'mysubs'), // mysubs | opensubtitles
        // Secret: set it in .env only, never in source.
        'opensubtitles_key' => env('OPENSUBTITLES_API_KEY', ''),
        'mysubs_base' => env('MYSUBS_BASE_URL', 'https://my-subs.co'),
        'languages' => ['en' => 'English', 'fa' => 'Persian', 'ar' => 'Arabic', 'tr' => 'Turkish', 'fr' => 'French', 'de' => 'German', 'es' => 'Spanish'],
    ],
];

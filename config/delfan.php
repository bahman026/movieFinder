<?php

// Delfan endpoint shape. Mirrors internal/delfan Options in the Go client:
// every value rotates across app releases, so each is an env override.
return [
    'login_host' => rtrim(env('DELFAN_LOGIN_HOST', 'http://googfilmazappfordownfilmmedis.xyz'), '/'),
    'api_host' => rtrim(env('DELFAN_API_HOST', 'http://tahlilgaraan.ir'), '/'),
    'base_path' => '/'.trim(env('DELFAN_BASE_PATH', '/app-plus'), '/'),
    'login_endpoint' => ltrim(env('DELFAN_LOGIN_ENDPOINT', 'users.php'), '/'),
    'api_endpoint' => ltrim(env('DELFAN_API_ENDPOINT', 'vp1.php'), '/'),
    'api_key' => env('DELFAN_API_KEY', 'pwep5d4sdoe0ewsosa7d563d'),
    'app_version' => env('DELFAN_APP_VERSION', 'c215sfxd545fgs'),
    'timeout' => (int) env('DELFAN_TIMEOUT', 30),
];

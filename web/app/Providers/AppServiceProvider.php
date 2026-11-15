<?php

namespace App\Providers;

use Illuminate\Cache\RateLimiting\Limit;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\RateLimiter;
use Illuminate\Support\ServiceProvider;

class AppServiceProvider extends ServiceProvider
{
    /**
     * Register any application services.
     */
    public function register(): void
    {
        //
    }

    /**
     * Bootstrap any application services.
     */
    public function boot(): void
    {
        // Named limiters, each with its OWN counter. A bare `throttle:N,1` keys
        // on the client IP alone, so every route shares one counter: a page's
        // ~40 poster requests to /img used up the page and download budgets
        // at once and the site answered 429.
        $per = fn (string $name) => fn (Request $r) => $name.'|'.$r->ip();

        RateLimiter::for('pages', fn (Request $r) => Limit::perMinute(120)->by($per('pages')($r)));
        RateLimiter::for('heavy', fn (Request $r) => Limit::perMinute(20)->by($per('heavy')($r)));
        RateLimiter::for('stream', fn (Request $r) => Limit::perMinute(600)->by($per('stream')($r)));
        RateLimiter::for('img', fn (Request $r) => Limit::perMinute(1200)->by($per('img')($r)));
    }
}

<?php

use App\Http\Controllers\CatalogController;
use App\Http\Controllers\ImageController;
use App\Http\Controllers\StreamController;
use Illuminate\Support\Facades\Route;

Route::middleware('throttle:pages')->group(function () {
    Route::get('/', [CatalogController::class, 'home'])->name('home');
    Route::get('/title/{src}/{id}', [CatalogController::class, 'show'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber('id')->name('title');
    Route::get('/cast', [CatalogController::class, 'cast'])->name('cast');
    Route::get('/cast/{id}', [CatalogController::class, 'castShow'])->whereNumber('id')->name('cast.show');
});

// Following links and fetching subtitle files cost the most upstream; keep them tighter.
Route::middleware('throttle:heavy')->group(function () {
    Route::get('/title/{src}/{id}/link/{index}', [CatalogController::class, 'resolve'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber(['id', 'index'])->name('resolve');
    Route::get('/subtitles/download', [CatalogController::class, 'subtitleDownload'])->name('subtitles.download');
    Route::get('/title/{src}/{id}/download/{index}', [CatalogController::class, 'download'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber(['id', 'index'])->name('download');
});

Route::middleware('throttle:pages')->group(function () {
    Route::get('/watch/{src}/{id}/{index}', [CatalogController::class, 'watch'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber(['id', 'index'])->name('watch');
    Route::get('/subtitles/vtt', [CatalogController::class, 'subtitleVtt'])->name('subtitles.vtt');
});

// Video bytes. No session: a stream can last hours, and a session saved when it
// ends would overwrite whatever the visitor did meanwhile. The token is the auth.
// A player sends many Range requests while seeking, hence its own limiter.
Route::get('/stream', [StreamController::class, 'stream'])
    ->withoutMiddleware('web')
    ->middleware('throttle:stream')->name('stream');

// Images: many per page, so they get their own, looser limit.
Route::get('/img', [ImageController::class, 'show'])->middleware('throttle:img')->name('img');

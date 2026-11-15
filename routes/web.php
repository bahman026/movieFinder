<?php

use App\Http\Controllers\CatalogController;
use Illuminate\Support\Facades\Route;

Route::middleware('throttle:60,1')->group(function () {
    Route::get('/', [CatalogController::class, 'home'])->name('home');
    Route::get('/title/{src}/{id}', [CatalogController::class, 'show'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber('id')->name('title');
    Route::get('/cast', [CatalogController::class, 'cast'])->name('cast');
    Route::get('/cast/{id}', [CatalogController::class, 'castShow'])->whereNumber('id')->name('cast.show');
    Route::post('/session/reset', [CatalogController::class, 'reset'])->name('reset');
});

// Following links and fetching subtitle files cost the most upstream; keep them tighter.
Route::middleware('throttle:15,1')->group(function () {
    Route::get('/title/{src}/{id}/link/{index}', [CatalogController::class, 'resolve'])
        ->whereIn('src', array_keys(CatalogController::SOURCES))->whereNumber(['id', 'index'])->name('resolve');
    Route::get('/subtitles/download', [CatalogController::class, 'subtitleDownload'])->name('subtitles.download');
});

<?php

use App\Http\Controllers\DelfanController;
use Illuminate\Support\Facades\Route;

Route::get('/', [DelfanController::class, 'home'])->name('home');
Route::get('/title/{id}', [DelfanController::class, 'show'])->name('title');
Route::get('/title/{id}/link/{index}', [DelfanController::class, 'resolve'])
    ->whereNumber(['id', 'index'])->name('resolve');
Route::post('/session/reset', [DelfanController::class, 'reset'])->name('reset');

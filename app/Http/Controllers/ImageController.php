<?php

namespace App\Http\Controllers;

use Illuminate\Contracts\Encryption\DecryptException;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use Throwable;

class ImageController extends Controller
{
    private const MAX_BYTES = 5 * 1024 * 1024;

    public function show(Request $request)
    {
        try {
            $url = decrypt((string) $request->query('u'), false);
        } catch (DecryptException) {
            abort(404);
        }
        abort_unless(is_string($url) && preg_match('#^https?://#i', $url), 404);

        // Posters are cheap to refetch; a day of file cache keeps upstream traffic low.
        $img = Cache::remember('img:'.sha1($url), 86400, function () use ($url) {
            try {
                $resp = Http::timeout(15)->withUserAgent('Mozilla/5.0')->accept('image/*')->get($url);
            } catch (Throwable) {
                return null;
            }
            $type = strtolower((string) $resp->header('Content-Type'));
            if (! $resp->successful() || ! str_starts_with($type, 'image/') || strlen($resp->body()) > self::MAX_BYTES) {
                return null;
            }

            return ['type' => $type, 'body' => base64_encode($resp->body())];
        });

        if (! $img) {
            Cache::forget('img:'.sha1($url)); // don't remember a failure for a day
            abort(404);
        }

        return response(base64_decode($img['body']), 200, [
            'Content-Type' => $img['type'],
            'Cache-Control' => 'public, max-age=86400',
            'X-Content-Type-Options' => 'nosniff',
        ]);
    }
}

<?php

namespace App\Support;

/**
 * Upstream images are served through /img on this site:
 *  - the upstream hosts never appear in the page, and
 *  - they are http-only, which an https page would block as mixed content.
 * The URL is encrypted with APP_KEY, so /img only fetches addresses this site
 * itself put on a page (it cannot be used as an open proxy).
 */
class Img
{
    public static function url(?string $upstream): string
    {
        $upstream = trim((string) $upstream);
        if (! preg_match('#^https?://#i', $upstream)) {
            return '';
        }

        return route('img', ['u' => encrypt($upstream, false)]);
    }
}

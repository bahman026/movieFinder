<?php

namespace App\Support;

/**
 * A /stream URL carries the upstream file address encrypted with APP_KEY and an
 * expiry, so the endpoint can run without a session (a session saved at the end
 * of an hour-long stream would overwrite everything the visitor did meanwhile)
 * and still only ever fetch addresses this site handed out: not an open proxy.
 */
class StreamToken
{
    public static function make(string $url, string $name, int $ttl = 6 * 3600): string
    {
        return encrypt(['u' => $url, 'n' => $name, 'e' => time() + $ttl], true);
    }

    /** @return array{u: string, n: string}|null */
    public static function read(string $token): ?array
    {
        try {
            $data = decrypt($token, true);
        } catch (\Throwable) {
            return null;
        }
        if (! is_array($data) || ($data['e'] ?? 0) < time() || ! preg_match('#^https?://#i', (string) ($data['u'] ?? ''))) {
            return null;
        }

        return ['u' => $data['u'], 'n' => (string) ($data['n'] ?? 'video')];
    }
}

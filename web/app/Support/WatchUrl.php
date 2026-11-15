<?php

namespace App\Support;

/** Link to a Watch page, on http when the player must stream straight from the database hosts. */
class WatchUrl
{
    public static function to(string $src, string $id, int $index, array $query = []): string
    {
        $url = route('watch', array_merge([$src, $id, $index], $query));

        return config('movies.watch_over_http') ? preg_replace('#^https://#i', 'http://', $url) : $url;
    }
}

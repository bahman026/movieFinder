<?php

namespace App\Support;

/** Video file names as shown and saved by this site, without the uploader's branding. */
class FileName
{
    /** "Movie.2026.720p.SoftSub.MovieJavan.mkv" -> "Movie.2026.720p.SoftSub.mkv" */
    public static function clean(string $name): string
    {
        $ext = pathinfo($name, PATHINFO_EXTENSION);
        $base = $ext !== '' ? substr($name, 0, -strlen($ext) - 1) : $name;
        $base = preg_replace('/[ ._\-\[\(]*(www\.)?movie[ ._\-]?javan(\.(com|net|ir|org))?[\]\)]*/i', '', $base);
        $base = trim(preg_replace('/([._\- ])\1+/', '$1', $base), ' ._-');

        return ($base !== '' ? $base : 'video').($ext !== '' ? '.'.$ext : '');
    }
}

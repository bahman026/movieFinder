<?php

namespace App\Services\Subtitles;

/** Loose title comparison shared by the subtitle sources (cf. matchingTitle in the Go app). */
class TitleMatch
{
    public static function normalize(string $s): string
    {
        $s = preg_replace(['/\(\d{4}\)/', '/^\s*\d{4}\s+-\s+/'], '', $s) ?? $s;

        return trim(preg_replace('/[^a-z0-9]+/', ' ', strtolower($s)) ?? '');
    }

    public static function same(string $a, string $b): bool
    {
        $a = self::normalize($a);
        $b = self::normalize($b);
        if ($a === '' || $b === '') {
            return false;
        }

        return $a === $b || str_starts_with($a, $b.' ') || str_starts_with($b, $a.' ');
    }
}

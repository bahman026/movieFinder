<?php

namespace App\Support;

/** SRT (or VTT) subtitle text to UTF-8 WebVTT, the only format a <track> accepts. */
class Vtt
{
    public static function fromSubtitle(string $data, string $lang = ''): string
    {
        $text = self::toUtf8($data, $lang);
        $text = preg_replace('/^\xEF\xBB\xBF/', '', $text);
        $text = str_replace(["\r\n", "\r"], "\n", $text);

        if (str_starts_with(ltrim($text), 'WEBVTT')) {
            return $text;
        }
        if (! preg_match('/\d{1,2}:\d{2}:\d{2}[,.]\d{1,3}\s*-->/', $text)) {
            throw new \RuntimeException('This subtitle is not in SRT format, so it cannot be shown in the browser player. Download it instead.');
        }

        // SRT timestamps use a comma before the milliseconds; WebVTT uses a dot.
        $text = preg_replace('/(\d{1,2}:\d{2}:\d{2}),(\d{1,3})/', '$1.$2', $text);

        return "WEBVTT\n\n".trim($text)."\n";
    }

    /**
     * Persian and Arabic subtitles are usually Windows-1256; most others are
     * Windows-1252 when not UTF-8. An MKV/VTT track is UTF-8 by definition.
     */
    public static function toUtf8(string $data, string $lang = ''): string
    {
        if (str_starts_with($data, "\xFF\xFE") || str_starts_with($data, "\xFE\xFF")) {
            return mb_convert_encoding($data, 'UTF-8', 'UTF-16');
        }
        if (mb_check_encoding($data, 'UTF-8')) {
            return $data;
        }
        $from = in_array(strtolower($lang), ['fa', 'ar', 'ur', 'per', 'ara'], true) || preg_match('/[\xC0-\xFF]{3,}/', $data)
            ? 'WINDOWS-1256' : 'WINDOWS-1252';
        $out = @iconv($from, 'UTF-8//IGNORE', $data);

        return $out !== false ? $out : mb_convert_encoding($data, 'UTF-8', 'ISO-8859-1');
    }
}

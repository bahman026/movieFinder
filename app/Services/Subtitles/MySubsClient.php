<?php

namespace App\Services\Subtitles;

use GuzzleHttp\Cookie\CookieJar;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;
use RuntimeException;
use ZipArchive;

/**
 * PHP port of internal/mysubs: a scraper for my-subs.co (no API, no key, no quota).
 *
 *   /search.php?key=…                                    two panels: Tv Shows, Movies
 *   /film-versions-{id}-{slug}-subtitles                 a movie's subtitles
 *   /versions-{id}-{episode}-{season}-{slug}-subtitles   one episode (EPISODE first!)
 *
 * /downloads/{token} is a gate page; the real path is in a REAL_URL JS variable,
 * and the file answers 302 without the PHPSESSID the gate sets — so both
 * requests share one cookie jar.
 */
class MySubsClient
{
    private const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

    private const ALIASES = [
        'en' => ['english'], 'fa' => ['persian', 'farsi'], 'ar' => ['arabic'], 'fr' => ['french'],
        'de' => ['german'], 'es' => ['spanish'], 'it' => ['italian'], 'tr' => ['turkish'],
        'ru' => ['russian'], 'pt' => ['portuguese'], 'nl' => ['dutch'], 'ja' => ['japanese'],
        'ko' => ['korean'], 'zh' => ['chinese'], 'he' => ['hebrew'], 'hi' => ['hindi'],
    ];

    private string $base;

    private CookieJar $jar;

    /** Pauses before each attempt; Cloudflare 52x and 502-504 are retried. */
    public array $retryWaits = [0, 2, 5];

    public function __construct(string $base = 'https://my-subs.co')
    {
        $this->base = rtrim($base, '/');
        $this->jar = new CookieJar;
    }

    public function search(string $title, string $year, string $lang, int $season = 0, int $episode = 0): array
    {
        $title = trim($title);
        if ($title === '') {
            throw new RuntimeException('Need a title to search.');
        }
        $wantSeries = $season > 0 && $episode > 0;

        $hits = $this->parseSearch($this->get('/search.php?key='.urlencode($title)));
        if (! $hits) {
            return [];
        }
        $best = $this->pickTitle($hits, $title, $year, $wantSeries);
        if (! TitleMatch::same($best['label'], $title)) {
            return [];
        }
        if ($best['series'] && ! $wantSeries) {
            throw new RuntimeException("\"{$best['label']}\" is a TV series here — pick a season and episode.");
        }

        $path = $best['path'];
        if ($best['series']) {
            if (! preg_match('#^/showlistsubtitles-(\d+)-(.+)$#', $path, $m)) {
                throw new RuntimeException("Unexpected series link $path");
            }
            $path = "/versions-{$m[1]}-{$episode}-{$season}-{$m[2]}-subtitles";
        }

        $subs = $this->parseSubtitlePage($this->get($path));
        $subs = $this->filterLanguage($subs, $lang);
        usort($subs, fn ($a, $b) => $b['downloads'] <=> $a['downloads']);

        return $subs;
    }

    /** @return array{0: string, 1: string} [content, file name] */
    public function download(string $ref): array
    {
        // Only follow the site's own gate pages: the ref comes back from the browser.
        if (! preg_match('#^/downloads/[A-Za-z0-9._\-/]+$#', $ref)) {
            throw new RuntimeException('Not a my-subs.co download link.');
        }
        $page = $this->get($ref);
        if (! preg_match('/REAL_URL\s*=\s*"((?:[^"\\\\]|\\\\.)*)"/', $page, $m)) {
            throw new RuntimeException('No download link on the page — the site layout may have changed.');
        }
        $real = strtr($m[1], ['\/' => '/', '\"' => '"', '\\\\' => '\\', "\\'" => "'"]);
        $resp = $this->request($real, $this->base.$ref);
        if (str_contains(strtolower($resp->header('Content-Type')), 'text/html')) {
            throw new RuntimeException('The site served a page instead of the subtitle file — try again.');
        }

        $name = '';
        if (preg_match('/filename\*?=(?:UTF-8\'\')?"?([^";]+)"?/i', $resp->header('Content-Disposition'), $d)) {
            $name = basename(rawurldecode($d[1]));
        }
        $name = $name ?: basename(parse_url($real, PHP_URL_PATH) ?: 'subtitle.srt');

        return $this->unzip($resp->body()) ?? [$resp->body(), $name];
    }

    // --- parsing --------------------------------------------------------------

    private function parseSearch(string $page): array
    {
        preg_match_all('#<a[^>]*href=[\'"](/(?:film-versions|showlistsubtitles)-[^\'"]+)[\'"][^>]*>(.*?)</a>#is', $page, $mm, PREG_SET_ORDER);

        return array_map(function ($m) {
            $label = $this->text($m[2]);
            preg_match('/\((\d{4})\)/', $label, $y);

            return ['path' => $m[1], 'label' => $label, 'year' => $y[1] ?? '', 'series' => str_starts_with($m[1], '/showlistsubtitles-')];
        }, $mm);
    }

    /** The site lists alphabetically, so "Breaking Bad Minisodes" precedes "Breaking Bad": score, don't take the first. */
    private function pickTitle(array $hits, string $title, string $year, bool $wantSeries): array
    {
        $want = TitleMatch::normalize($title);
        $best = $hits[0];
        $bestScore = PHP_INT_MIN;
        foreach ($hits as $h) {
            $got = TitleMatch::normalize($h['label']);
            $score = match (true) {
                $got === $want => 100,
                str_starts_with($got, $want) || str_starts_with($want, $got) => 60,
                str_contains($got, $want) || str_contains($want, $got) => 30,
                default => -20,
            };
            $score -= intdiv(strlen($got), 8);
            $score += $h['series'] === $wantSeries ? 50 : -50;
            if ($year !== '' && $h['year'] !== '') {
                $score += $h['year'] === $year ? 40 : -15;
            }
            if ($score > $bestScore) {
                [$best, $bestScore] = [$h, $score];
            }
        }

        return $best;
    }

    private function parseSubtitlePage(string $page): array
    {
        preg_match('#<h1[^>]*>(.*?)</h1>#is', $page, $h);
        $heading = trim(preg_replace('/^Movie\s+|\s*Subtitles$/', '', $this->text($h[1] ?? '')));
        preg_match('/\((\d{4})\)/', $heading, $y);
        $title = trim(preg_replace('/\(\d{4}\)/', '', $heading));

        $subs = [];
        // Movie pages: one anchor per subtitle.
        preg_match_all('#<a[^>]*href=["\'](/downloads/[^"\']+)["\'][^>]*class=["\']list-group-item["\'][^>]*>(.*?)</a>#is', $page, $mm, PREG_SET_ORDER);
        foreach ($mm as $m) {
            preg_match('#title=["\']([^"\']+)["\']#is', $m[2], $lang);
            preg_match('#<strong>(.*?)</strong>#is', $m[2], $rel);
            preg_match('#<b>\s*(\d+)\s*</b>#is', $m[2], $n);
            $subs[] = ['ref' => $m[1], 'language' => $this->text($lang[1] ?? ''), 'release' => $this->text($rel[1] ?? ''), 'downloads' => (int) ($n[1] ?? 0)];
        }

        // Episode pages: rows grouped under <div class='version'>.
        if (! $subs) {
            $chunks = preg_split('#<div class=[\'"]version[\'"]>#is', $page);
            foreach (array_slice($chunks, 1) as $chunk) {
                preg_match('#<i>(.*?)</i>#is', $chunk, $rel);
                $release = $this->text($rel[1] ?? '');
                preg_match_all('#<div class=[\'"]lang[\'"]>.*?<i>(.*?)</i>.*?(?:Downloads\s*:</b>\s*(\d+).*?)?href=[\'"](/downloads/[^"\']+)[\'"]#is', $chunk, $rows, PREG_SET_ORDER);
                foreach ($rows as $r) {
                    $subs[] = ['ref' => $r[3], 'language' => $this->text($r[1]), 'release' => $release, 'downloads' => (int) ($r[2] ?? 0)];
                }
            }
        }

        return array_map(fn ($s) => $s + ['provider' => 'mysubs', 'title' => $title, 'year' => $y[1] ?? ''], $subs);
    }

    private function filterLanguage(array $subs, string $code): array
    {
        $code = strtolower(trim($code));
        if ($code === '') {
            return $subs;
        }
        $names = self::ALIASES[$code] ?? [$code];

        return array_values(array_filter($subs, function ($s) use ($names) {
            foreach ($names as $n) {
                if (str_contains(strtolower($s['language']), $n)) {
                    return true;
                }
            }

            return false;
        }));
    }

    private function unzip(string $data): ?array
    {
        if (! str_starts_with($data, "PK\x03\x04") || ! class_exists(ZipArchive::class)) {
            return null;
        }
        $tmp = tempnam(sys_get_temp_dir(), 'sub');
        file_put_contents($tmp, $data);
        $zip = new ZipArchive;
        try {
            if ($zip->open($tmp) !== true) {
                return null;
            }
            for ($i = 0; $i < $zip->numFiles; $i++) {
                $name = $zip->getNameIndex($i);
                if (preg_match('/\.(srt|ass|ssa|sub|vtt)$/i', $name) && ($content = $zip->getFromIndex($i))) {
                    return [$content, basename($name)];
                }
            }

            return null;
        } finally {
            @$zip->close();
            @unlink($tmp);
        }
    }

    private function text(string $html): string
    {
        $s = html_entity_decode(preg_replace('/<[^>]*>/s', ' ', $html), ENT_QUOTES | ENT_HTML5);

        return trim(preg_replace('/\s+/u', ' ', $s));
    }

    // --- transport ----------------------------------------------------------

    private function get(string $path): string
    {
        return $this->request($path, $this->base.'/')->body();
    }

    private function request(string $ref, string $referer)
    {
        $url = preg_match('#^https?://#', $ref) ? $ref : $this->base.'/'.ltrim($ref, '/');
        $last = null;
        foreach ($this->retryWaits as $i => $wait) {
            if ($i > 0 && $wait > 0) {
                sleep($wait);
            }
            try {
                $resp = Http::timeout(30)
                    ->withOptions(['cookies' => $this->jar])
                    ->withUserAgent(self::UA)
                    ->withHeaders(['Accept' => 'text/html,application/xhtml+xml,*/*;q=0.8', 'Accept-Language' => 'en-US,en;q=0.9', 'Referer' => $referer])
                    ->get($url);
            } catch (ConnectionException $e) {
                throw new RuntimeException('my-subs.co is unreachable.');
            }
            if ($resp->successful()) {
                return $resp;
            }
            $status = $resp->status();
            $last = new RuntimeException('my-subs.co answered '.$status.($status >= 520 && $status <= 524 ? ' (Cloudflare could not reach the site; it is down or overloaded)' : '').'.');
            if (! in_array($status, [502, 503, 504, 520, 521, 522, 523, 524], true)) {
                break;
            }
        }
        throw $last;
    }
}

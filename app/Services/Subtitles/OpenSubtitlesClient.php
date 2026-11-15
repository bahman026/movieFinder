<?php

namespace App\Services\Subtitles;

use Illuminate\Support\Facades\Http;
use RuntimeException;

/**
 * PHP port of internal/opensubtitles. Needs a free API key (OPENSUBTITLES_API_KEY);
 * without one the API answers "You cannot consume this service". Anonymous
 * downloads are capped at 5/day per IP (the server's IP, on a website).
 */
class OpenSubtitlesClient
{
    private const BASE = 'https://api.opensubtitles.com/api/v1';

    public const REGISTER_URL = 'https://www.opensubtitles.com/en/consumers';

    public function __construct(private string $apiKey) {}

    /**
     * IMDb id first (parent_imdb_id for an episode), then title + year: the two
     * are indexed independently, so one being empty says nothing about the other.
     */
    public function search(string $title, string $imdbId, string $year, string $lang, int $season = 0, int $episode = 0): array
    {
        $this->requireKey();
        $lang = $lang ?: 'en';
        $episodic = $season > 0 && $episode > 0;
        $imdbId = ltrim(preg_replace('/\D/', '', $imdbId) ?? '', '0');

        if ($imdbId !== '') {
            $q = ['languages' => $lang, $episodic ? 'parent_imdb_id' : 'imdb_id' => $imdbId];
            $subs = $this->query($q + $this->episode($season, $episode));
            if ($subs) {
                return $subs;
            }
        }

        $title = trim($title);
        if ($title === '') {
            throw new RuntimeException('Need a title or an IMDb id to search.');
        }
        $q = ['query' => $title, 'languages' => $lang] + $this->episode($season, $episode);
        if ($year !== '' && ! $episodic) {
            $q['year'] = $year;
        }

        // Free-text search is fuzzy and returns other films: keep only matches.
        return array_values(array_filter($this->query($q), fn ($s) => TitleMatch::same($s['title'], $title)
            && ($episodic || $year === '' || $s['year'] === '' || abs((int) $s['year'] - (int) $year) <= 1)));
    }

    /** @return array{0: string, 1: string} [content, file name] */
    public function download(int $fileId): array
    {
        $this->requireKey();
        $resp = $this->http()->post(self::BASE.'/download', ['file_id' => $fileId]);
        $this->check($resp);
        $link = $resp->json('link');
        if (! $link) {
            throw new RuntimeException($resp->json('message') ?: 'No download link in the response.');
        }
        $file = Http::timeout(20)->get($link);
        if (! $file->successful()) {
            throw new RuntimeException('Subtitle file: server answered '.$file->status().'.');
        }

        return [$file->body(), $resp->json('file_name') ?: 'subtitle.srt'];
    }

    private function query(array $q): array
    {
        $resp = $this->http()->get(self::BASE.'/subtitles', $q);
        $this->check($resp);

        $out = [];
        foreach ($resp->json('data') ?? [] as $item) {
            $a = $item['attributes'] ?? [];
            $file = $a['files'][0] ?? null;
            if (! $file) {
                continue;
            }
            $fd = $a['feature_details'] ?? [];
            $out[] = [
                'provider' => 'opensubtitles',
                'ref' => (string) $file['file_id'],
                'language' => (string) ($a['language'] ?? ''),
                'release' => (string) ($a['release'] ?? ''),
                // movie_name reads "2010 - Inception"; title is the bare name, and an
                // episode's show name is in parent_title.
                'title' => (string) (($fd['parent_title'] ?? '') ?: ($fd['title'] ?? '') ?: ($fd['movie_name'] ?? '')),
                'year' => (string) ($fd['year'] ?? ''),
                'downloads' => (int) ($a['download_count'] ?? 0),
            ];
        }

        return $out;
    }

    private function episode(int $season, int $episode): array
    {
        return $season > 0 && $episode > 0 ? ['season_number' => $season, 'episode_number' => $episode] : [];
    }

    private function http()
    {
        return Http::timeout(20)->acceptJson()->asJson()
            ->withHeaders(['Api-Key' => $this->apiKey])
            ->withUserAgent('MovieFinderWeb v0.1');
    }

    private function check($resp): void
    {
        if (! $resp->successful()) {
            throw new RuntimeException('OpenSubtitles: '.($resp->json('message') ?: 'answered '.$resp->status()));
        }
    }

    private function requireKey(): void
    {
        if (trim($this->apiKey) === '') {
            throw new RuntimeException('OpenSubtitles needs an API key (OPENSUBTITLES_API_KEY). Get a free one at '.self::REGISTER_URL);
        }
    }
}

<?php

namespace App\Services\Playstore;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use RuntimeException;

/**
 * PHP port of internal/api (Go): the "playstore" movie API.
 *
 * Quirks kept from the Go client (see the repo's CLAUDE.md):
 *  - every scalar is a JSON string, including numbers and flags;
 *  - errors arrive with HTTP 200 as {"status":"error","message":"..."};
 *  - search is not paged and splits results into movie/tvseries/tv_channels;
 *  - images in detail responses point at a dead CDN, so they are rewritten
 *    onto the host that is answering;
 *  - hosts is an ordered mirror list, and the host that answered last is
 *    remembered (shared via cache) so a dead mirror costs one failed request.
 */
class PlaystoreClient
{
    private const ACTIVE_KEY = 'playstore:active-host';

    public function __construct(private array $cfg) {}

    public function movies(int $page = 1): array
    {
        return array_map(fn ($m) => $this->item($m), $this->list($this->get('get_movies', ['page' => max(1, $page)])));
    }

    public function search(string $query): array
    {
        $query = trim($query);
        if ($query === '') {
            return [];
        }
        $resp = $this->get('search', ['q' => $query]);
        $all = array_merge($resp['movie'] ?? [], $resp['tvseries'] ?? [], $resp['tv_channels'] ?? []);

        return array_map(fn ($m) => $this->item($m), $all);
    }

    public function slider(): array
    {
        return array_map(fn ($m) => $this->item($m), $this->get('get_slider')['data'] ?? []);
    }

    public function details(string $kind, string $id): array
    {
        $kind = $kind === 'tvseries' ? 'tvseries' : 'movie';
        $d = $this->get('get_single_details', ['type' => $kind, 'id' => $id]);

        $links = array_map(fn ($l) => [
            'name' => $this->linkName($l),
            'size' => $this->sizeLabel($l['file_size'] ?? null),
            'url' => (string) ($l['download_url'] ?? ''),
        ], $d['download_links'] ?? []);

        // Some titles only carry streamable "videos", not download links.
        if (! $links) {
            $links = array_map(fn ($v) => [
                'name' => trim(($v['label'] ?? '').' '.($v['file_type'] ?? '')),
                'size' => $this->sizeLabel($v['file_size'] ?? null),
                'url' => (string) ($v['file_url'] ?? ''),
            ], $d['videos'] ?? []);
        }
        $links = array_values(array_filter($links, fn ($l) => $l['url'] !== ''));

        $seasons = [];
        foreach ($d['season'] ?? [] as $s) {
            $eps = [];
            foreach ($s['episodes'] ?? [] as $e) {
                if (! empty($e['file_url'])) {
                    $eps[] = ['name' => (string) ($e['episodes_name'] ?? ''), 'size' => $this->sizeLabel($e['file_size'] ?? null), 'url' => $e['file_url']];
                }
            }
            if ($eps) {
                $seasons[] = ['name' => (string) ($s['seasons_name'] ?? ''), 'episodes' => $eps];
            }
        }

        $names = fn ($k) => array_values(array_filter(array_map(fn ($n) => $n['name'] ?? '', $d[$k] ?? [])));

        return [
            'source' => 'playstore',
            'id' => (string) ($d['videos_id'] ?? $id),
            'kind' => $kind,
            'title' => (string) ($d['title'] ?? ''),
            // `writer` holds the localized title, not a writer credit.
            'local_title' => (string) ($d['writer'] ?? ''),
            'original_title' => (string) ($d['title'] ?? ''),
            'imdb_id' => (string) ($d['imdbid'] ?? ''),
            'rating' => (string) ($d['imdb_rating'] ?? ''),
            'year' => substr((string) ($d['release'] ?? ''), 0, 4),
            'description' => trim(strip_tags(str_replace(['<br>', '<br/>', '<br />'], "\n", (string) ($d['description'] ?? '')))),
            'poster' => $this->fixImage((string) ($d['poster_url'] ?? $d['thumbnail_url'] ?? '')),
            'genres' => $names('genre'),
            'cast' => array_slice($names('cast'), 0, 12),
            'is_movie' => $kind === 'movie',
            'links' => $kind === 'movie' ? $links : [],
            'seasons' => $seasons,
        ];
    }

    // --- transport ----------------------------------------------------------

    /** GET endpoint on the first host that answers, starting from the last good one. */
    private function get(string $endpoint, array $params = []): array
    {
        $hosts = $this->hosts();
        if (! $hosts) {
            throw new RuntimeException('No playstore hosts configured (PLAYSTORE_HOSTS).');
        }
        $query = array_merge($this->commonParams(), $params);
        $start = (int) Cache::get(self::ACTIVE_KEY, 0);
        if ($start < 0 || $start >= count($hosts)) {
            $start = 0;
        }

        $failures = [];
        foreach (array_keys($hosts) as $offset) {
            $i = ($start + $offset) % count($hosts);
            $host = $hosts[$i];
            try {
                $resp = Http::timeout($this->cfg['timeout'])
                    ->accept('application/json')
                    ->withUserAgent('Mozilla/5.0')
                    ->get($host.$this->cfg['base_path'].'/'.$endpoint, $query);
            } catch (ConnectionException $e) {
                $failures[] = "$host: unreachable";

                continue; // transport failure: what the mirror list is for
            }

            if ($resp->serverError() || trim($resp->body()) === '') {
                $failures[] = "$host: ".($resp->serverError() ? 'answered '.$resp->status() : 'empty response');

                continue;
            }
            if (! $resp->successful()) {
                // A 4xx would fail the same way on every mirror.
                throw new RuntimeException("$host answered ".$resp->status().'.');
            }

            Cache::forever(self::ACTIVE_KEY, $i);
            $json = $resp->json();
            if (! is_array($json)) {
                throw new RuntimeException('The playstore API sent something that is not JSON.');
            }
            // Errors arrive with HTTP 200.
            if (strcasecmp((string) ($json['status'] ?? ''), 'error') === 0) {
                throw new RuntimeException((string) ($json['message'] ?? '') ?: 'The API reported an error.');
            }

            return $json;
        }

        throw new RuntimeException('All playstore hosts failed: '.implode('; ', $failures));
    }

    private function commonParams(): array
    {
        $p = [
            'api_secret_key' => $this->cfg['secret_key'],
            'version' => $this->cfg['version'],
            'country' => $this->cfg['country'],
        ];
        if ($this->cfg['sp']) {
            $p['sp'] = 'true';
        }

        return $p;
    }

    private function hosts(): array
    {
        return array_values(array_filter(array_map(
            fn ($h) => rtrim(trim($h), '/'),
            explode(',', $this->cfg['hosts'])
        )));
    }

    private function activeHost(): string
    {
        $hosts = $this->hosts();

        return $hosts[(int) Cache::get(self::ACTIVE_KEY, 0)] ?? ($hosts[0] ?? '');
    }

    /** The detail CDN is dead; the same /playstore/uploads path is served by the API hosts. */
    private function fixImage(string $url): string
    {
        $path = parse_url($url, PHP_URL_PATH);
        if ($path && str_contains($path, '/uploads/') && ! str_starts_with($url, $this->activeHost())) {
            return $this->activeHost().$path;
        }

        return $url;
    }

    // --- shaping --------------------------------------------------------------

    private function list(array $json): array
    {
        return array_is_list($json) ? $json : [];
    }

    private function item(array $m): array
    {
        return [
            'id' => (string) ($m['videos_id'] ?? ''),
            'title' => (string) ($m['title'] ?? ''),
            'subtitle' => (string) ($m['writer'] ?? ''),
            'year' => substr((string) ($m['release'] ?? ''), 0, 4),
            'rating' => (string) ($m['imdb_rating'] ?? ''),
            'thumb' => (string) (($m['thumbnail_url'] ?? '') ?: ($m['poster_url'] ?? '')),
            'kind' => ($m['is_tvseries'] ?? '') === '1' ? 'tvseries' : 'movie',
        ];
    }

    /** The real quality is in `label`; `resolution` is a decorative glyph unless it has a digit. */
    private function linkName(array $l): string
    {
        $parts = [trim((string) ($l['label'] ?? ''))];
        $res = trim((string) ($l['resolution'] ?? ''));
        if ($res !== '' && preg_match('/\d/', $res)) {
            $parts[] = $res;
        }

        return implode(' · ', array_filter($parts));
    }

    /** file_size is a bare number of megabytes, frequently null. */
    private function sizeLabel(mixed $mb): string
    {
        $mb = is_numeric(trim((string) $mb)) ? (float) $mb : 0;
        if ($mb <= 0) {
            return '';
        }

        return $mb >= 1024 ? sprintf('%.1f GB', $mb / 1024) : sprintf('%.0f MB', $mb);
    }
}

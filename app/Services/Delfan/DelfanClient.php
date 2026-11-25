<?php

namespace App\Services\Delfan;

use Illuminate\Contracts\Session\Session;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use RuntimeException;

/**
 * PHP port of internal/delfan (Go). See that package's doc for the signing:
 *
 *  - body: built once per session from the login auth token.
 *  - an:   rolling nonce MD5(q1 + q2 + 101); every gated request must carry the
 *          nonce derived from the PREVIOUS response's q1/q2.
 *
 * Privacy: nothing identifying is hardcoded. Each browser session gets a
 * freshly random android_id and a randomly picked generic User-Agent, and the
 * whole upstream session (auth token, nonce state) lives only in this
 * visitor's encrypted Laravel session. It is never logged or written to the DB,
 * and reset() throws it away.
 */
class DelfanClient
{
    private const MID1 = 'fdaa94a151e2c5d4';
    private const MID2 = '74a290e8';
    private const TAIL = 'y87mdjsodon';
    private const NONCE_CONST = 101;

    private const SESSION_KEY = 'delfan';

    /** Generic Android UAs; one is picked per session, none tied to a person. */
    private const USER_AGENTS = [
        'Dalvik/2.1.0 (Linux; U; Android 11; Pixel 4a Build/RQ3A.211001.001)',
        'Dalvik/2.1.0 (Linux; U; Android 12; SM-A525F Build/SP1A.210812.016)',
        'Dalvik/2.1.0 (Linux; U; Android 10; Redmi Note 8 Build/QKQ1.200114.002)',
        'Dalvik/2.1.0 (Linux; U; Android 13; M2101K6G Build/TKQ1.221013.002)',
    ];

    public function __construct(private Session $session, private array $cfg) {}

    /** Landing rows, flattened and de-duplicated by id. */
    public function home(): array
    {
        $seen = [];
        $items = [];
        foreach ($this->homeRows() as $row) {
            foreach ($row as $it) {
                if ($it['id'] === '' || isset($seen[$it['id']])) {
                    continue;
                }
                $seen[$it['id']] = true;
                $items[] = $it;
            }
        }

        return $items;
    }

    /**
     * The landing rows as the app groups them, plus "top": every title on the
     * landing page ordered by IMDb rating. The API has no top-rated endpoint
     * (its sort_by values are unknown), so this is the best of what is new.
     *
     * @return array{new_movies: array, new_series: array, updated_series: array, featured: array, top: array}
     */
    public function homeRows(): array
    {
        $rows = $this->locked(function () {
            $resp = $this->roundtrip('vitrin', []);
            $map = fn ($g) => array_values(array_filter(array_map(fn ($it) => $this->item($it), $resp[$g] ?? []), fn ($it) => $it['id'] !== ''));

            return ['new_movies' => $map('NewMovie'), 'new_series' => $map('NewSerie'),
                'updated_series' => $map('updated_serie'), 'featured' => $map('vige')];
        });

        $all = [];
        foreach ($rows as $row) {
            foreach ($row as $it) {
                if ((float) $it['rating'] > 0) {
                    $all[$it['id']] ??= $it;
                }
            }
        }
        $all = array_values($all);
        usort($all, fn ($a, $b) => (float) $b['rating'] <=> (float) $a['rating']);
        $rows['top'] = array_slice($all, 0, 20);

        return $rows;
    }

    public function search(string $query, int $page = 1): array
    {
        $query = trim($query);
        if ($query === '') {
            return [];
        }

        return $this->locked(function () use ($query, $page) {
            $form = array_fill_keys(['country', 'year_to', 'sort_by', 'type', 'dub', 'year_from', 'imdb', 'genre', 'StateSerie'], '');
            $form['search_text'] = $query;
            $resp = $this->roundtrip('filter_search&pageno='.max(1, $page), $form);
            $this->assertOk($resp);

            return array_map(fn ($it) => $this->item($it), $resp['all'] ?? []);
        });
    }

    /** One title with its links: ['links' => [...]] for films, ['seasons' => [...]] for series. */
    public function details(string $id): array
    {
        return $this->locked(function () use ($id) {
            $resp = $this->roundtrip('detials', ['is_mobile' => '1', 'id' => $id]);
            $this->assertOk($resp);
            $d = $resp['detiles'][0] ?? throw new RuntimeException("No details returned for id {$id}.");

            $isMovie = ($d['is_movie'] ?? '') === '1';
            $raw = $d['download_link'] ?? [];
            $raw = is_string($raw) ? (json_decode($raw, true) ?: []) : $raw;

            $links = [];
            $seasons = [];
            if ($isMovie) {
                $links = array_map(fn ($l) => $this->link($l), $raw);
            } else {
                foreach ($raw as $s) {
                    if (! empty($s['links'])) {
                        $seasons[] = ['name' => $s['session_title'] ?? '', 'episodes' => array_map(fn ($l) => $this->link($l), $s['links'])];
                    }
                }
            }

            $desc = (string) ($d['description'] ?? '');

            $original = $this->originalTitle($desc);

            return [
                'source' => 'delfan',
                'id' => $id,
                'kind' => $isMovie ? 'movie' : 'tvseries',
                'title' => $original ?: ($d['title'] ?? ''),
                'local_title' => $d['title'] ?? '',
                'original_title' => $original,
                'imdb_id' => '',
                'rating' => $d['imdb_rating'] ?? '',
                'genres' => [],
                'cast' => [],
                'description' => trim(str_replace(['<br>', '<br/>', '<br />', "\r"], ["\n", "\n", "\n", ''], $desc)),
                'year' => $d['year'] ?? '',
                'poster' => ($d['poster_url'] ?? '') ?: ($d['thumbnail_url'] ?? ''),
                'is_movie' => $isMovie,
                'links' => $links,
                'seasons' => $seasons,
            ];
        });
    }

    /** People (actors/directors) matching a name. */
    public function searchCast(string $name): array
    {
        $name = trim($name);
        if ($name === '') {
            return [];
        }

        return $this->locked(function () use ($name) {
            $resp = $this->roundtrip('search_cast&pageno=1', ['q' => $name, 'cast_type' => 'cast']);
            $this->assertOk($resp);

            return array_map(fn ($p) => [
                'id' => (string) (int) ($p['id'] ?? 0),
                'name' => (string) ($p['name'] ?? ''),
                'role' => (string) ($p['action_user'] ?? ''),
                'pic' => (string) ($p['pic_url'] ?? ''),
            ], $resp['movie_list'] ?? []);
        });
    }

    /** One page of a person's titles, plus their bio. */
    public function castMovies(string $castId, int $page = 1): array
    {
        return $this->locked(function () use ($castId, $page) {
            $resp = $this->roundtrip('show_movie_cast&pageno='.max(1, $page), ['cast_id' => $castId, 'cast_type' => 'cast']);
            $this->assertOk($resp);

            return [
                'items' => array_map(fn ($it) => $this->item($it), $resp['movie_list'] ?? []),
                'bio' => trim(strip_tags(str_replace('<br>', "\n", (string) ($resp['bio'] ?? '')))),
            ];
        });
    }

    /**
     * Follows a play.php link's 302 to the real signed file URL without fetching
     * the file. The URL expires, so it is resolved on demand and never stored.
     */
    public function resolve(string $playUrl): string
    {
        $resp = Http::withoutRedirecting()
            ->timeout($this->cfg['timeout'])
            ->withUserAgent($this->state()['ua'] ?? self::USER_AGENTS[0])
            ->get($playUrl);

        if ($loc = $resp->header('Location')) {
            return $loc;
        }
        if ($resp->successful()) {
            return $playUrl;
        }
        throw new RuntimeException('Could not resolve the link (server answered '.$resp->status().').');
    }

    /** Forget the upstream session entirely; the next call logs in fresh. */
    public function reset(): void
    {
        $this->session->forget(self::SESSION_KEY);
    }

    // --- session & signing ------------------------------------------------

    /**
     * Requests on one session must serialize: two concurrent calls would reuse
     * a nonce and one would be rejected. A cache lock keyed by the (hashed)
     * session id does what the Go client's mutex did.
     */
    private function locked(callable $fn): mixed
    {
        $lock = Cache::lock('delfan:'.hash('sha256', $this->session->getId()), 60);

        return $lock->block(30, function () use ($fn) {
            $this->ensureSession();
            try {
                return $fn();
            } catch (RuntimeException $e) {
                // "N - error identifying connection": the upstream session or
                // nonce went stale (expired login, rotated host). Log in fresh
                // once and retry; a second failure is a real error.
                if (! str_contains($e->getMessage(), 'error identifying connection')) {
                    throw $e;
                }
                $this->reset();
                $this->ensureSession();

                return $fn();
            }
        });
    }

    private function ensureSession(): void
    {
        if (! empty($this->state()['auth'])) {
            return;
        }

        $state = [
            // Random per session: nothing ties requests to a real device.
            'android_id' => bin2hex(random_bytes(8)),
            'ua' => self::USER_AGENTS[random_int(0, count(self::USER_AGENTS) - 1)],
        ];
        $this->session->put(self::SESSION_KEY, $state);

        $login = $this->post($this->loginUrl(), [
            'user_name' => '', 'apname' => 'Delfan', 'is_tv' => 'No',
            'app_verion' => '1', 'android_id' => $state['android_id'],
            'version_sp' => 'v1', 'token' => '',
        ]);

        $auth = $login['infos'][0]['auth'] ?? '';
        if ($auth === '') {
            $this->reset();
            throw new RuntimeException('Login returned no session token.');
        }

        $state += [
            'auth' => $auth,
            'us' => (string) ($login['night_mode'] ?? ''),
            'body' => $this->buildBody($auth),
            'q1' => (int) ($login['q1'] ?? 0),
            'q2' => (int) ($login['q2'] ?? 0),
        ];
        $this->session->put(self::SESSION_KEY, $state);

        // The nonce is per-host: a vitrin call on the gated host seeds its
        // q1/q2. vitrin does not validate the nonce, so the seed is free.
        try {
            $this->roundtrip('vitrin', []);
        } catch (\Throwable $e) {
            $this->reset();
            throw new RuntimeException('Session seed failed: '.$e->getMessage(), 0, $e);
        }
    }

    /** Sign with the current nonce, send, then advance the nonce from the reply. */
    private function roundtrip(string $action, array $form): array
    {
        $s = $this->state();
        $form += [
            's_n' => '1', 'user_name' => '', 'u_s' => $s['us'], 'apname' => 'Delfan',
            'body' => $s['body'], 'langueg' => '', 'token' => '',
            'an' => md5((string) ($s['q1'] + $s['q2'] + self::NONCE_CONST)),
        ];

        $resp = $this->post($this->actionUrl($action), $form);

        if (array_key_exists('q1', $resp)) {
            $s['q1'] = (int) $resp['q1'];
            $s['q2'] = (int) ($resp['q2'] ?? 0);
            $this->session->put(self::SESSION_KEY, $s);
        }

        return $resp;
    }

    private function post(string $url, array $form): array
    {
        $resp = Http::asForm()
            ->timeout($this->cfg['timeout'])
            ->withoutRedirecting()
            ->withUserAgent($this->state()['ua'] ?? self::USER_AGENTS[0])
            ->post($url, $form);

        if (! $resp->successful()) {
            throw new RuntimeException('Server answered '.$resp->status().'.');
        }
        $json = $resp->json();
        if (! is_array($json)) {
            throw new RuntimeException('Server sent a response that is not JSON.');
        }

        return $json;
    }

    private function buildBody(string $auth): string
    {
        $head = md5(random_int(0, 499).'cotation');
        $tail = md5(date('D M d H:i:s T Y').'cotation');

        return $head.$auth.self::MID1.self::MID2.$auth.self::TAIL.$this->cfg['app_version'].$tail;
    }

    private function loginUrl(): string
    {
        return $this->cfg['login_host'].$this->cfg['base_path'].'/'.$this->cfg['login_endpoint']
            .'?key='.$this->cfg['api_key'].'&action=login';
    }

    private function actionUrl(string $action): string
    {
        return $this->cfg['api_host'].$this->cfg['base_path'].'/'.$this->cfg['api_endpoint']
            .'?key='.$this->cfg['api_key'].'&action='.$action;
    }

    private function state(): array
    {
        return $this->session->get(self::SESSION_KEY, []);
    }

    private function assertOk(array $resp): void
    {
        if (strcasecmp((string) ($resp['state_all'] ?? ''), 'F') === 0) {
            throw new RuntimeException(trim((string) ($resp['msg'] ?? '')) ?: 'The server rejected the request.');
        }
    }

    private function item(array $it): array
    {
        return [
            'id' => (string) ($it['videos_id'] ?? ''),
            'title' => $it['title'] ?? '',
            'subtitle' => '',
            'year' => (string) ($it['year'] ?? ''),
            'rating' => (string) ($it['imdb'] ?? ''),
            'thumb' => $it['thumbnail_url'] ?? '',
            'kind' => '',
        ];
    }

    private function link(array $l): array
    {
        return [
            'name' => trim((string) ($l['name'] ?? '')),
            'size' => trim((string) ($l['video_size'] ?? '')),
            'url' => (string) ($l['link'] ?? ''),
        ];
    }

    private function originalTitle(string $desc): string
    {
        $marker = 'نام اصلی :';
        $i = mb_strpos($desc, $marker);
        if ($i === false) {
            return '';
        }
        $rest = mb_substr($desc, $i + mb_strlen($marker));
        if (($j = mb_strpos($rest, '<br>')) !== false) {
            $rest = mb_substr($rest, 0, $j);
        }

        return trim($rest);
    }
}

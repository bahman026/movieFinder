<?php

namespace App\Http\Controllers;

use App\Services\Delfan\DelfanClient;
use App\Services\Playstore\PlaystoreClient;
use App\Services\Subtitles\MySubsClient;
use App\Services\Subtitles\OpenSubtitlesClient;
use App\Support\StreamToken;
use App\Support\Vtt;
use Illuminate\Support\Facades\Cache;
use Symfony\Component\HttpKernel\Exception\HttpException;
use Illuminate\Http\Request;
use Throwable;

/**
 * Both movie sources behind one set of pages. Each client returns the same item
 * and detail shape, so the views never know which source they are showing (as
 * delfan_adapt.go does for the desktop app).
 */
class CatalogController extends Controller
{
    /** URL slug => label. The upstream services are never named on the site. */
    public const SOURCES = ['db1' => 'Database 1', 'db2' => 'Database 2'];

    private function delfan(Request $request): DelfanClient
    {
        return new DelfanClient($request->session(), config('delfan'));
    }

    private function playstore(): PlaystoreClient
    {
        return new PlaystoreClient(config('movies.playstore'));
    }

    /** ?src= switches the source and the choice is remembered for the session. */
    private function source(Request $request): string
    {
        $src = (string) $request->query('src', '');
        if (isset(self::SOURCES[$src])) {
            $request->session()->put('source', $src);
        }
        $src = $request->session()->get('source', config('movies.default_source'));

        return isset(self::SOURCES[$src]) ? $src : 'db1';
    }

    public function home(Request $request)
    {
        $src = $this->source($request);
        $q = trim((string) $request->query('q', ''));
        $page = max(1, (int) $request->query('page', 1));
        $slider = [];
        $paged = true;

        try {
            if ($src === 'db2') {
                $items = $q === '' ? $this->delfan($request)->home() : $this->delfan($request)->search($q, $page);
                $paged = $q !== '';
            } else {
                $pc = $this->playstore();
                if ($q !== '') {
                    $items = $pc->search($q);
                    $paged = false; // playstore search is not paged
                } else {
                    $items = $pc->movies($page);
                    if ($page === 1) {
                        try {
                            $slider = $pc->slider();
                        } catch (Throwable) {
                            // The slider is decoration; never let it sink the page.
                        }
                    }
                }
            }
            $error = null;
        } catch (Throwable $e) {
            $items = [];
            $error = $this->explain($e, $src);
        }

        return view('site.index', compact('items', 'slider', 'q', 'page', 'error', 'src', 'paged'));
    }

    public function show(Request $request, string $src, string $id)
    {
        abort_unless(isset(self::SOURCES[$src]) && ctype_digit($id), 404);
        $kind = $request->query('kind') === 'tvseries' ? 'tvseries' : 'movie';

        try {
            $detail = $src === 'db2'
                ? $this->delfan($request)->details($id)
                : $this->playstore()->details($kind, $id);
        } catch (Throwable $e) {
            return view('site.show', ['detail' => null, 'error' => $this->explain($e, $src), 'src' => $src]);
        }

        // Remember which URLs this title offered, so the link route only
        // follows those (an index, never a URL from the browser).
        $all = $detail['links'];
        foreach ($detail['seasons'] as $s) {
            array_push($all, ...$s['episodes']);
        }
        $request->session()->put("links.$src.$id", array_column($all, 'url'));
        // What the watch page needs to search subtitles, without another upstream call.
        $request->session()->put("titles.$src.$id", [
            'title' => $detail['title'], 'original_title' => $detail['original_title'],
            'local_title' => $detail['local_title'], 'imdb_id' => $detail['imdb_id'],
            'year' => $detail['year'], 'is_movie' => $detail['is_movie'], 'kind' => $detail['kind'],
            'labels' => array_map(fn ($l) => trim($l['name'].' '.$l['size']), $all),
        ]);

        [$subs, $subError, $subForm] = $this->subtitles($request, $detail);

        return view('site.show', [
            'detail' => $detail, 'error' => null, 'src' => $src,
            'subs' => $subs, 'subError' => $subError, 'subForm' => $subForm,
            'languages' => config('movies.subtitles.languages'),
        ]);
    }

    /** Subtitle search for the title page, run only when the form was submitted. */
    private function subtitles(Request $request, array $detail, int $defSeason = 0, int $defEpisode = 0): array
    {
        $form = [
            'provider' => in_array($request->query('subs'), ['mysubs', 'opensubtitles'], true)
                ? $request->query('subs') : config('movies.subtitles.default_provider'),
            'lang' => array_key_exists((string) $request->query('lang'), config('movies.subtitles.languages'))
                ? $request->query('lang') : 'en',
            'season' => max(0, (int) $request->query('season', $defSeason)),
            'episode' => max(0, (int) $request->query('episode', $defEpisode)),
            'searched' => $request->has('subs'),
        ];
        if (! $form['searched']) {
            return [null, null, $form];
        }

        $title = $detail['original_title'] ?: $detail['title'];
        $series = ! $detail['is_movie'];
        if ($series && ($form['season'] < 1 || $form['episode'] < 1)) {
            return [[], 'Pick a season and episode number to search subtitles for a series.', $form];
        }
        [$s, $e] = $series ? [$form['season'], $form['episode']] : [0, 0];

        try {
            $subs = $form['provider'] === 'opensubtitles'
                ? (new OpenSubtitlesClient(config('movies.subtitles.opensubtitles_key')))->search($title, $detail['imdb_id'], $detail['year'], $form['lang'], $s, $e)
                : (new MySubsClient(config('movies.subtitles.mysubs_base')))->search($title, $detail['year'], $form['lang'], $s, $e);

            return [array_slice($subs, 0, 40), null, $form];
        } catch (Throwable $ex) {
            $other = $form['provider'] === 'opensubtitles' ? 'my-subs.co' : 'OpenSubtitles';

            return [[], $ex->getMessage()." Try $other instead.", $form];
        }
    }

    /**
     * One subtitle file, cached for a day: the player asks for the track again on
     * every play, and each OpenSubtitles fetch costs one of the 5 daily downloads.
     *
     * @return array{0: string, 1: string} [content, file name]
     */
    private function fetchSubtitle(string $provider, string $ref): array
    {
        abort_unless(in_array($provider, ['opensubtitles', 'mysubs'], true), 404);
        abort_if($provider === 'opensubtitles' && ! ctype_digit($ref), 404);

        $key = 'sub:'.sha1($provider.'|'.$ref);
        if ($hit = Cache::get($key)) {
            return [base64_decode($hit[0]), $hit[1]];
        }
        [$data, $name] = $provider === 'opensubtitles'
            ? (new OpenSubtitlesClient(config('movies.subtitles.opensubtitles_key')))->download((int) $ref)
            : (new MySubsClient(config('movies.subtitles.mysubs_base')))->download($ref);
        Cache::put($key, [base64_encode($data), $name], 86400);

        return [$data, $name];
    }

    public function subtitleDownload(Request $request)
    {
        try {
            [$data, $name] = $this->fetchSubtitle((string) $request->query('provider'), (string) $request->query('ref'));
        } catch (HttpException $e) {
            throw $e;
        } catch (Throwable $e) {
            return back()->with('error', 'Subtitle download failed: '.$e->getMessage());
        }

        // ?name=Movie.2010.720p.fa saves the subtitle next to the video under the
        // video's own name, so VLC, MX Player, PotPlayer and MPC load it by
        // themselves. Text is UTF-8 with a BOM: Windows players then show
        // Persian correctly instead of guessing a legacy code page.
        if (($base = trim((string) $request->query('name'))) !== '') {
            $ext = strtolower(pathinfo($name, PATHINFO_EXTENSION));
            $ext = in_array($ext, ['srt', 'ass', 'ssa', 'vtt', 'sub'], true) ? $ext : 'srt';
            $name = $base.'.'.$ext;
            $data = "\xEF\xBB\xBF".preg_replace('/^\xEF\xBB\xBF/', '', Vtt::toUtf8($data, (string) $request->query('lang')));
        }

        // An upstream-supplied name must not be able to break the header or path.
        $name = preg_replace('/[^\w.\- ]+/u', '_', basename($name)) ?: 'subtitle.srt';

        return response($data, 200, [
            'Content-Type' => 'application/octet-stream',
            'Content-Disposition' => 'attachment; filename="'.$name.'"',
        ]);
    }

    /** The chosen subtitle as UTF-8 WebVTT for the player's <track>. */
    public function subtitleVtt(Request $request)
    {
        try {
            [$data] = $this->fetchSubtitle((string) $request->query('provider'), (string) $request->query('ref'));
            $vtt = Vtt::fromSubtitle($data, (string) $request->query('lang'));
        } catch (HttpException $e) {
            throw $e;
        } catch (Throwable $e) {
            return response("WEBVTT\n\nNOTE ".str_replace("\n", ' ', $e->getMessage())."\n", 200, ['Content-Type' => 'text/vtt; charset=utf-8']);
        }

        return response($vtt, 200, ['Content-Type' => 'text/vtt; charset=utf-8', 'Cache-Control' => 'private, max-age=3600']);
    }

    /** The playable URL behind link $index of a title shown in this session. */
    private function playUrl(Request $request, string $src, string $id, int $index): string
    {
        $url = $request->session()->get("links.$src.$id")[$index] ?? null;
        abort_if(! $url, 404);

        // Database 1 links are already the signed file URL; Database 2 links
        // are play.php redirects that are resolved here.
        return $src === 'db2' ? $this->delfan($request)->resolve($url) : $url;
    }

    /**
     * Release names say what a browser will choke on: x265/HEVC video, and
     * AC3/E-AC3/DTS/TrueHD (often marked 6CH or 5.1) audio. Most phones and
     * desktop browsers play only H.264 video with AAC audio.
     */
    private static function formatWarning(string $file): ?string
    {
        if (preg_match('/x265|hevc|h\.?265/i', $file)) {
            return 'This file is x265/HEVC video, which most browsers can\'t play.';
        }
        if (preg_match('/(^|[^a-z0-9])(ac3|e-?ac3|ddp?\d?|dts|truehd|atmos|6ch|5\.1|7\.1)([^a-z0-9]|$)/i', $file)) {
            return 'This file probably has AC3/DTS (surround) audio, which many browsers, phones especially, can\'t play.';
        }

        return null;
    }

    private static function fileName(string $url): string
    {
        return rawurldecode(basename((string) parse_url($url, PHP_URL_PATH))) ?: 'video.mp4';
    }

    public function watch(Request $request, string $src, string $id, int $index)
    {
        // Direct streaming needs an http page (see config movies.watch_over_http).
        if (config('movies.watch_over_http') && $request->secure()) {
            return redirect()->to(preg_replace('#^https://#i', 'http://', $request->fullUrl()));
        }

        $meta = $request->session()->get("titles.$src.$id");
        if (! $meta) {
            return redirect()->route('title', array_filter([$src, $id, 'kind' => $request->query('kind')]));
        }
        try {
            $url = $this->playUrl($request, $src, $id, $index);
        } catch (HttpException $e) {
            throw $e;
        } catch (Throwable $e) {
            return redirect()->route('title', [$src, $id])->with('error', $e->getMessage());
        }
        $file = self::fileName($url);
        $token = StreamToken::make($url, $file);

        // An episode's file name carries SxxEyy: prefill the subtitle search with it.
        [$season, $episode] = preg_match('/S(\d{1,2})[ ._-]?E(\d{1,3})/i', $file, $m) ? [(int) $m[1], (int) $m[2]] : [0, 0];
        [$subs, $subError, $subForm] = $this->subtitles($request, $meta, $season, $episode);

        $chosen = null;
        if (preg_match('/^(mysubs|opensubtitles):(.+)$/', (string) $request->query('sub'), $m)) {
            $chosen = ['provider' => $m[1], 'ref' => $m[2], 'label' => (string) $request->query('sublabel', 'Subtitle')];
        }

        return view('site.watch', [
            'src' => $src, 'id' => $id, 'index' => $index, 'meta' => $meta, 'file' => $file,
            'label' => $meta['labels'][$index] ?? $file,
            'fileBase' => pathinfo($file, PATHINFO_FILENAME),
            'streamUrl' => route('stream', ['t' => $token]),
            'directUrl' => $url,
            'formatWarning' => self::formatWarning($file),
            'subs' => $subs, 'subError' => $subError, 'subForm' => $subForm, 'chosen' => $chosen,
            'languages' => config('movies.subtitles.languages'),
        ]);
    }

    /** Direct link to the file, for an external player such as VLC or PotPlayer. */
    public function resolve(Request $request, string $src, string $id, int $index)
    {
        try {
            $url = $this->playUrl($request, $src, $id, $index);
        } catch (HttpException $e) {
            throw $e;
        } catch (Throwable $e) {
            return $request->boolean('json')
                ? response()->json(['error' => $e->getMessage()], 502)
                : back()->with('error', $e->getMessage());
        }

        return $request->boolean('json') ? response()->json(['url' => $url]) : redirect()->away($url);
    }

    public function cast(Request $request)
    {
        $q = trim((string) $request->query('q', ''));
        try {
            $people = $this->delfan($request)->searchCast($q);
            $error = null;
        } catch (Throwable $e) {
            [$people, $error] = [[], $this->explain($e, 'db2')];
        }

        return view('site.cast', ['people' => $people, 'q' => $q, 'error' => $error, 'src' => 'db2']);
    }

    public function castShow(Request $request, string $id)
    {
        abort_unless(ctype_digit($id), 404);
        $page = max(1, (int) $request->query('page', 1));
        try {
            $r = $this->delfan($request)->castMovies($id, $page);
            $error = null;
        } catch (Throwable $e) {
            [$r, $error] = [['items' => [], 'bio' => ''], $this->explain($e, 'db2')];
        }

        return view('site.cast_show', [
            'items' => $r['items'], 'bio' => $r['bio'], 'name' => (string) $request->query('name', ''),
            'id' => $id, 'page' => $page, 'error' => $error, 'src' => 'db2',
        ]);
    }


    /** Turn a failure into a message that says what to do, especially when hosts rotated. */
    private function explain(Throwable $e, string $src): string
    {
        $msg = $e->getMessage();
        $unreachable = str_contains($msg, 'host') || str_contains($msg, 'cURL') || str_contains($msg, 'resolve')
            || str_contains($msg, 'identifying connection') || str_contains($msg, 'answered 5');
        if (! $unreachable) {
            return $msg;
        }
        // Server-side detail (which .env setting to change) goes to the log, not the page.
        logger()->warning('upstream unreachable', ['source' => $src, 'error' => $msg,
            'hint' => $src === 'db2' ? 'update DELFAN_LOGIN_HOST / DELFAN_API_HOST' : 'update PLAYSTORE_HOSTS']);

        return self::SOURCES[$src].' is not reachable right now. Try again later, or switch to the other database above.';
    }
}

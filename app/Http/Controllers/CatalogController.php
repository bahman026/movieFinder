<?php

namespace App\Http\Controllers;

use App\Services\Delfan\DelfanClient;
use App\Services\Playstore\PlaystoreClient;
use App\Services\Subtitles\MySubsClient;
use App\Services\Subtitles\OpenSubtitlesClient;
use Illuminate\Http\Request;
use Throwable;

/**
 * Both movie sources behind one set of pages. Each client returns the same item
 * and detail shape, so the views never know which source they are showing (as
 * delfan_adapt.go does for the desktop app).
 */
class CatalogController extends Controller
{
    public const SOURCES = ['playstore' => 'MovieFinder', 'delfan' => 'Delfan'];

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

        return isset(self::SOURCES[$src]) ? $src : 'playstore';
    }

    public function home(Request $request)
    {
        $src = $this->source($request);
        $q = trim((string) $request->query('q', ''));
        $page = max(1, (int) $request->query('page', 1));
        $slider = [];
        $paged = true;

        try {
            if ($src === 'delfan') {
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

        return view('delfan.index', compact('items', 'slider', 'q', 'page', 'error', 'src', 'paged'));
    }

    public function show(Request $request, string $src, string $id)
    {
        abort_unless(isset(self::SOURCES[$src]) && ctype_digit($id), 404);
        $kind = $request->query('kind') === 'tvseries' ? 'tvseries' : 'movie';

        try {
            $detail = $src === 'delfan'
                ? $this->delfan($request)->details($id)
                : $this->playstore()->details($kind, $id);
        } catch (Throwable $e) {
            return view('delfan.show', ['detail' => null, 'error' => $this->explain($e, $src), 'src' => $src]);
        }

        // Remember which URLs this title offered, so the link route only
        // follows those (an index, never a URL from the browser).
        $all = $detail['links'];
        foreach ($detail['seasons'] as $s) {
            array_push($all, ...$s['episodes']);
        }
        $request->session()->put("links.$src.$id", array_column($all, 'url'));

        [$subs, $subError, $subForm] = $this->subtitles($request, $detail);

        return view('delfan.show', [
            'detail' => $detail, 'error' => null, 'src' => $src,
            'subs' => $subs, 'subError' => $subError, 'subForm' => $subForm,
            'languages' => config('movies.subtitles.languages'),
        ]);
    }

    /** Subtitle search for the title page, run only when the form was submitted. */
    private function subtitles(Request $request, array $detail): array
    {
        $form = [
            'provider' => in_array($request->query('subs'), ['mysubs', 'opensubtitles'], true)
                ? $request->query('subs') : config('movies.subtitles.default_provider'),
            'lang' => array_key_exists((string) $request->query('lang'), config('movies.subtitles.languages'))
                ? $request->query('lang') : 'en',
            'season' => max(0, (int) $request->query('season', 0)),
            'episode' => max(0, (int) $request->query('episode', 0)),
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

    public function subtitleDownload(Request $request)
    {
        $provider = (string) $request->query('provider');
        $ref = (string) $request->query('ref');

        try {
            [$data, $name] = match ($provider) {
                'opensubtitles' => ctype_digit($ref)
                    ? (new OpenSubtitlesClient(config('movies.subtitles.opensubtitles_key')))->download((int) $ref)
                    : abort(404),
                'mysubs' => (new MySubsClient(config('movies.subtitles.mysubs_base')))->download($ref),
                default => abort(404),
            };
        } catch (Throwable $e) {
            return back()->with('error', 'Subtitle download failed: '.$e->getMessage());
        }

        // An upstream-supplied name must not be able to break the header or path.
        $name = preg_replace('/[^\w.\- ]+/u', '_', basename($name)) ?: 'subtitle.srt';

        return response($data, 200, [
            'Content-Type' => 'application/octet-stream',
            'Content-Disposition' => 'attachment; filename="'.$name.'"',
        ]);
    }

    public function resolve(Request $request, string $src, string $id, int $index)
    {
        $url = $request->session()->get("links.$src.$id")[$index] ?? null;
        abort_if(! $url, 404);

        if ($src !== 'delfan') {
            // Playstore links are already the signed file URL.
            return redirect()->away($url);
        }

        try {
            return redirect()->away($this->delfan($request)->resolve($url));
        } catch (Throwable $e) {
            return back()->with('error', $e->getMessage());
        }
    }

    public function cast(Request $request)
    {
        $q = trim((string) $request->query('q', ''));
        try {
            $people = $this->delfan($request)->searchCast($q);
            $error = null;
        } catch (Throwable $e) {
            [$people, $error] = [[], $this->explain($e, 'delfan')];
        }

        return view('delfan.cast', ['people' => $people, 'q' => $q, 'error' => $error, 'src' => 'delfan']);
    }

    public function castShow(Request $request, string $id)
    {
        abort_unless(ctype_digit($id), 404);
        $page = max(1, (int) $request->query('page', 1));
        try {
            $r = $this->delfan($request)->castMovies($id, $page);
            $error = null;
        } catch (Throwable $e) {
            [$r, $error] = [['items' => [], 'bio' => ''], $this->explain($e, 'delfan')];
        }

        return view('delfan.cast_show', [
            'items' => $r['items'], 'bio' => $r['bio'], 'name' => (string) $request->query('name', ''),
            'id' => $id, 'page' => $page, 'error' => $error, 'src' => 'delfan',
        ]);
    }

    public function reset(Request $request)
    {
        $this->delfan($request)->reset();
        $request->session()->forget('links');
        $request->session()->regenerate();

        return redirect()->route('home')->with('status', 'Session cleared. A new anonymous login will be made on the next request.');
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
        $hint = $src === 'delfan'
            ? 'The Delfan servers may have moved: update DELFAN_LOGIN_HOST / DELFAN_API_HOST in .env.'
            : 'The playstore mirrors may have moved: update PLAYSTORE_HOSTS in .env.';

        return $msg.' '.$hint.' You can also switch to the other source above.';
    }
}

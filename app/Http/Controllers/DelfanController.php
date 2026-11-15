<?php

namespace App\Http\Controllers;

use App\Services\Delfan\DelfanClient;
use Illuminate\Http\Request;
use Throwable;

class DelfanController extends Controller
{
    private function client(Request $request): DelfanClient
    {
        return new DelfanClient($request->session(), config('delfan'));
    }

    public function home(Request $request)
    {
        $q = trim((string) $request->query('q', ''));
        $page = max(1, (int) $request->query('page', 1));

        try {
            $items = $q === ''
                ? $this->client($request)->home()
                : $this->client($request)->search($q, $page);
            $error = null;
        } catch (Throwable $e) {
            $items = [];
            $error = $e->getMessage();
        }

        return view('delfan.index', compact('items', 'q', 'page', 'error'));
    }

    public function show(Request $request, string $id)
    {
        abort_unless(ctype_digit($id), 404);

        try {
            $detail = $this->client($request)->details($id);
        } catch (Throwable $e) {
            return view('delfan.show', ['detail' => null, 'error' => $e->getMessage()]);
        }

        // Remember which play.php URLs this title offered, so /resolve only
        // follows links the upstream actually gave us (no open redirect/SSRF).
        $all = $detail['links'];
        foreach ($detail['seasons'] as $s) {
            array_push($all, ...$s['episodes']);
        }
        $request->session()->put("delfan_links.$id", array_column($all, 'url'));

        return view('delfan.show', ['detail' => $detail, 'error' => null]);
    }

    public function resolve(Request $request, string $id, int $index)
    {
        $url = $request->session()->get("delfan_links.$id")[$index] ?? null;
        abort_if(! $url, 404);

        try {
            return redirect()->away($this->client($request)->resolve($url));
        } catch (Throwable $e) {
            return back()->with('error', $e->getMessage());
        }
    }

    public function reset(Request $request)
    {
        $this->client($request)->reset();
        $request->session()->forget('delfan_links');
        $request->session()->regenerate();

        return redirect()->route('home')->with('status', 'Session cleared. A new anonymous login will be made on the next request.');
    }
}

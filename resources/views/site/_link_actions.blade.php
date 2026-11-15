<span class="actions">
    <a class="btn primary" href="{{ \App\Support\WatchUrl::to($src, $detail['id'], $i) }}">▶ Watch</a>
    {{-- Straight from the database host: costs this server no traffic. The host is http-only,
         so a browser may ask to "Keep" the file; download managers take it as is. --}}
    <a class="btn" href="{{ route('resolve', [$src, $detail['id'], $i]) }}" rel="noreferrer nofollow"
       title="Direct from {{ \App\Http\Controllers\CatalogController::SOURCES[$src] }}. If the browser warns, choose Keep.">⬇ Download</a>
    <button type="button" class="btn ghost" data-copy-resolve="{{ route('resolve', [$src, $detail['id'], $i, 'json' => 1]) }}"
            title="Copy the file address for IDM, ADM, VLC or PotPlayer">Copy link</button>
</span>

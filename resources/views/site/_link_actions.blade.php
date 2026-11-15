<span class="actions">
    <a class="btn primary" href="{{ route('watch', [$src, $detail['id'], $i]) }}">▶ Watch</a>
    <a class="btn" href="{{ route('download', [$src, $detail['id'], $i]) }}" rel="nofollow">Download</a>
    <a class="btn ghost" href="{{ route('resolve', [$src, $detail['id'], $i]) }}" rel="noreferrer nofollow" title="Plain file address, for VLC, PotPlayer or a download manager">Direct link</a>
</span>

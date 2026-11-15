<a class="card" href="{{ route('title', array_filter([$src, $it['id'], 'kind' => $it['kind'] ?: null])) }}">
    <img src="{{ \App\Support\Img::url($it['thumb']) }}" alt="" loading="lazy" referrerpolicy="no-referrer">
    <div dir="auto">
        {{ $it['title'] }}
        @if ($it['subtitle'] && $it['subtitle'] !== $it['title'])<br><span class="muted">{{ $it['subtitle'] }}</span>@endif
        <br><span class="muted">{{ $it['year'] }}@if ($it['rating']) · IMDb {{ $it['rating'] }}@endif @if ($it['kind'] === 'tvseries') · Series @endif</span>
    </div>
</a>

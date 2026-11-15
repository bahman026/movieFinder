@extends('delfan.layout')

@section('title', $detail['title'] ?? 'Title')

@section('content')
    @if ($error)
        <p class="error">{{ $error }}</p>
        <a class="btn" href="{{ route('home') }}">Back</a>
    @else
        @php $n = 0; @endphp
        <div class="detail">
            <img src="{{ $detail['poster'] }}" alt="" referrerpolicy="no-referrer">
            <div>
                <h1 dir="auto">{{ $detail['title'] }}</h1>
                @if ($detail['local_title'] && $detail['local_title'] !== $detail['title'])
                    <p dir="auto" style="margin-top:-10px">{{ $detail['local_title'] }}</p>
                @endif
                <p class="muted">
                    {{ $detail['year'] }}
                    @if ($detail['rating']) · IMDb {{ $detail['rating'] }} @endif
                    · {{ $detail['is_movie'] ? 'Movie' : 'Series' }}
                    @if ($detail['imdb_id'])
                        · <a href="https://www.imdb.com/title/{{ $detail['imdb_id'] }}/" rel="noreferrer noopener" target="_blank">IMDb page</a>
                    @endif
                </p>
                @if ($detail['genres'])
                    <div class="tags">@foreach ($detail['genres'] as $g)<span dir="auto">{{ $g }}</span>@endforeach</div>
                @endif
                <pre dir="auto">{{ $detail['description'] }}</pre>
                @if ($detail['cast'])
                    <p class="muted" dir="auto">Cast: {{ implode(', ', $detail['cast']) }}</p>
                @endif

                @if ($detail['is_movie'])
                    <h3>Links</h3>
                    <ul class="links">
                        @forelse ($detail['links'] as $l)
                            <li><span dir="auto">{{ $l['name'] ?: 'Link '.($n + 1) }} <span class="muted">{{ $l['size'] }}</span></span>
                                <a class="btn" href="{{ route('resolve', [$src, $detail['id'], $n++]) }}" rel="noreferrer">Open</a></li>
                        @empty
                            <li class="muted">No links.</li>
                        @endforelse
                    </ul>
                @else
                    <h3>Episodes</h3>
                    @forelse ($detail['seasons'] as $s)
                        <details>
                            <summary dir="auto">{{ $s['name'] }}</summary>
                            <ul class="links">
                                @foreach ($s['episodes'] as $l)
                                    <li><span dir="auto">{{ $l['name'] }} <span class="muted">{{ $l['size'] }}</span></span>
                                        <a class="btn" href="{{ route('resolve', [$src, $detail['id'], $n++]) }}" rel="noreferrer">Open</a></li>
                                @endforeach
                            </ul>
                        </details>
                    @empty
                        <p class="muted">No episodes.</p>
                    @endforelse
                @endif

                <section class="subs">
                    <h3>Subtitles</h3>
                    <form action="{{ route('title', array_filter([$src, $detail['id'], 'kind' => $src === 'playstore' ? $detail['kind'] : null])) }}#subs" method="get" id="subs">
                        @if ($src === 'playstore') <input type="hidden" name="kind" value="{{ $detail['kind'] }}"> @endif
                        <select name="subs" aria-label="Subtitle source">
                            <option value="mysubs" @selected($subForm['provider'] === 'mysubs')>my-subs.co</option>
                            <option value="opensubtitles" @selected($subForm['provider'] === 'opensubtitles')>OpenSubtitles</option>
                        </select>
                        <select name="lang" aria-label="Language">
                            @foreach ($languages as $code => $label)
                                <option value="{{ $code }}" @selected($subForm['lang'] === $code)>{{ $label }}</option>
                            @endforeach
                        </select>
                        @unless ($detail['is_movie'])
                            <input type="number" name="season" min="1" placeholder="Season" value="{{ $subForm['season'] ?: '' }}" aria-label="Season">
                            <input type="number" name="episode" min="1" placeholder="Episode" value="{{ $subForm['episode'] ?: '' }}" aria-label="Episode">
                        @endunless
                        <button type="submit">Find subtitles</button>
                    </form>
                    <p class="muted" style="font-size:13px">Searching for “{{ $detail['original_title'] ?: $detail['title'] }}”. OpenSubtitles allows 5 downloads a day; my-subs.co has no limit.</p>

                    @if ($subError)
                        <p class="error">{{ $subError }}</p>
                    @elseif (is_array($subs))
                        <ul class="links">
                            @forelse ($subs as $sub)
                                <li>
                                    <span><span dir="auto">{{ $sub['release'] ?: $sub['title'] }}</span>
                                        <span class="muted">{{ $sub['language'] }}@if ($sub['downloads']) · {{ number_format($sub['downloads']) }} downloads @endif</span></span>
                                    <a class="btn" href="{{ route('subtitles.download', ['provider' => $sub['provider'], 'ref' => $sub['ref']]) }}" rel="nofollow">Download</a>
                                </li>
                            @empty
                                <li class="muted">No subtitles found for this title in that language.</li>
                            @endforelse
                        </ul>
                    @endif
                </section>
            </div>
        </div>
    @endif
@endsection

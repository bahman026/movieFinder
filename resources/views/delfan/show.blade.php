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
                <p class="muted">
                    @if ($detail['original_title']) {{ $detail['original_title'] }} · @endif
                    {{ $detail['year'] }}
                    @if ($detail['imdb_rating']) · IMDb {{ $detail['imdb_rating'] }} @endif
                    · {{ $detail['is_movie'] ? 'Movie' : 'Series' }}
                </p>
                <pre dir="auto">{{ $detail['description'] }}</pre>

                @if ($detail['is_movie'])
                    <h3>Links</h3>
                    <ul class="links">
                        @forelse ($detail['links'] as $l)
                            <li><span dir="auto">{{ $l['name'] }} <span class="muted">{{ $l['size'] }}</span></span>
                                <a class="btn" href="{{ route('resolve', [$detail['id'], $n++]) }}" rel="noreferrer">Open</a></li>
                        @empty
                            <li class="muted">No links.</li>
                        @endforelse
                    </ul>
                @else
                    @forelse ($detail['seasons'] as $s)
                        <details>
                            <summary dir="auto">{{ $s['name'] }}</summary>
                            <ul class="links">
                                @foreach ($s['episodes'] as $l)
                                    <li><span dir="auto">{{ $l['name'] }} <span class="muted">{{ $l['size'] }}</span></span>
                                        <a class="btn" href="{{ route('resolve', [$detail['id'], $n++]) }}" rel="noreferrer">Open</a></li>
                                @endforeach
                            </ul>
                        </details>
                    @empty
                        <p class="muted">No episodes.</p>
                    @endforelse
                @endif
            </div>
        </div>
    @endif
@endsection

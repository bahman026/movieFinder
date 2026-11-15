@extends('delfan.layout')

@section('content')
    @if ($error)
        <p class="error">{{ $error }}</p>
    @endif

    <h2>{{ $q === '' ? 'Latest' : 'Results for “'.$q.'”' }}</h2>

    @if (empty($items) && ! $error)
        <p class="muted">Nothing found.</p>
    @endif

    <div class="grid">
        @foreach ($items as $it)
            <a class="card" href="{{ route('title', $it['id']) }}">
                <img src="{{ $it['thumb'] }}" alt="" loading="lazy" referrerpolicy="no-referrer">
                <div dir="auto">
                    {{ $it['title'] }}
                    <span class="muted">{{ $it['year'] }}@if ($it['imdb']) · IMDb {{ $it['imdb'] }}@endif</span>
                </div>
            </a>
        @endforeach
    </div>

    @if ($q !== '')
        <nav class="pager">
            @if ($page > 1)
                <a class="btn" href="{{ route('home', ['q' => $q, 'page' => $page - 1]) }}">Previous</a>
            @endif
            <span class="muted">Page {{ $page }}</span>
            @if (! empty($items))
                <a class="btn" href="{{ route('home', ['q' => $q, 'page' => $page + 1]) }}">Next</a>
            @endif
        </nav>
    @endif
@endsection

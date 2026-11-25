@extends('site.layout')

@section('content')
    @if ($error)
        <p class="error">{{ $error }}</p>
    @endif

    @if ($slider)
        <h2>Featured</h2>
        <div class="slider">
            @foreach ($slider as $it) @include('site._card') @endforeach
        </div>
    @endif

    @foreach ($rows as $row)
        <div class="row-head">
            <h2>{{ $row['title'] }}</h2>
            @if ($row['more'])<a href="{{ $row['more'] }}">See all</a>@endif
        </div>
        <div class="slider">
            @foreach ($row['items'] as $it) @include('site._card') @endforeach
        </div>
    @endforeach

    @if ($heading !== null)
        <h2>{{ $heading }}</h2>

        @if (empty($items) && ! $error)
            <p class="muted">Nothing found.</p>
        @endif

        <div class="grid">
            @foreach ($items as $it) @include('site._card') @endforeach
        </div>
    @endif

    @if ($paged)
        <nav class="pager">
            @if ($page > 1)
                <a class="btn" href="{{ route('home', array_filter(['q' => $q, 'list' => $list, 'page' => $page - 1])) }}">Previous</a>
            @endif
            <span class="muted">Page {{ $page }}</span>
            @if (! empty($items))
                <a class="btn" href="{{ route('home', array_filter(['q' => $q, 'list' => $list, 'page' => $page + 1])) }}">Next</a>
            @endif
        </nav>
    @endif
@endsection

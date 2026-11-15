@extends('site.layout')

@section('title', $name ?: 'Actor')

@section('content')
    <h1 dir="auto">{{ $name }}</h1>
    @if ($error) <p class="error">{{ $error }}</p> @endif
    @if ($bio && $page === 1) <pre dir="auto" class="muted">{{ $bio }}</pre> @endif
    @if (! $items && ! $error) <p class="muted">No titles for this person.</p> @endif
    <div class="grid">
        @foreach ($items as $it) @include('site._card') @endforeach
    </div>
    <nav class="pager">
        @if ($page > 1)
            <a class="btn" href="{{ route('cast.show', [$id, 'name' => $name, 'page' => $page - 1]) }}">Previous</a>
        @endif
        <span class="muted">Page {{ $page }}</span>
        @if ($items)
            <a class="btn" href="{{ route('cast.show', [$id, 'name' => $name, 'page' => $page + 1]) }}">Next</a>
        @endif
    </nav>
@endsection

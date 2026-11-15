@extends('delfan.layout')

@section('title', 'Actors')

@section('content')
    <form action="{{ route('cast') }}" method="get" style="display:flex;gap:6px;margin-bottom:16px">
        <input type="search" name="q" value="{{ $q }}" placeholder="Actor or director name" dir="auto" style="flex:1">
        <button type="submit">Find</button>
    </form>
    @if ($error) <p class="error">{{ $error }}</p> @endif
    @if ($q !== '' && ! $people && ! $error) <p class="muted">Nobody found.</p> @endif
    <div class="people">
        @foreach ($people as $p)
            <a class="card" href="{{ route('cast.show', [$p['id'], 'name' => $p['name']]) }}">
                <img src="{{ $p['pic'] }}" alt="" loading="lazy" referrerpolicy="no-referrer">
                <div dir="auto">{{ $p['name'] }} <span class="muted">{{ $p['role'] }}</span></div>
            </a>
        @endforeach
    </div>
@endsection

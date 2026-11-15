<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="referrer" content="no-referrer">
    <title>@yield('title', 'MovieFinder Web')</title>
    <style>
        :root { --bg:#f6f6f4; --fg:#1d1d1b; --muted:#6b6b66; --card:#fff; --line:#e3e3df; --accent:#2f6fd6; --err:#b3261e; }
        @media (prefers-color-scheme: dark) { :root { --bg:#161615; --fg:#ececea; --muted:#9a9a94; --card:#20201f; --line:#33332f; --accent:#7aa7ff; --err:#ff8a80; } }
        * { box-sizing: border-box; }
        body { margin:0; font:15px/1.5 system-ui, -apple-system, "Segoe UI", Tahoma, sans-serif; background:var(--bg); color:var(--fg); }
        header { display:flex; gap:12px; align-items:center; flex-wrap:wrap; padding:12px 16px; border-bottom:1px solid var(--line); background:var(--card); }
        header a.brand { font-weight:600; color:var(--fg); text-decoration:none; }
        header form.search { display:flex; gap:6px; flex:1; min-width:200px; }
        input[type=search] { flex:1; padding:8px 10px; border:1px solid var(--line); border-radius:6px; background:var(--bg); color:var(--fg); }
        button, .btn { padding:8px 12px; border:1px solid var(--line); border-radius:6px; background:var(--card); color:var(--fg); cursor:pointer; text-decoration:none; font:inherit; }
        main { max-width:1100px; margin:0 auto; padding:16px; }
        .grid { display:grid; grid-template-columns:repeat(auto-fill, minmax(150px, 1fr)); gap:14px; }
        .card { background:var(--card); border:1px solid var(--line); border-radius:8px; overflow:hidden; color:inherit; text-decoration:none; }
        .card img { width:100%; aspect-ratio:2/3; object-fit:cover; display:block; background:var(--line); }
        .card div { padding:8px; font-size:14px; }
        .muted { color:var(--muted); }
        .error { color:var(--err); border:1px solid var(--err); padding:10px; border-radius:6px; }
        .status { border:1px solid var(--line); padding:10px; border-radius:6px; }
        .detail { display:grid; grid-template-columns:220px 1fr; gap:20px; }
        .detail img { width:100%; border-radius:8px; }
        @media (max-width:640px) { .detail { grid-template-columns:1fr; } .detail img { max-width:220px; } }
        ul.links { list-style:none; padding:0; }
        ul.links li { display:flex; justify-content:space-between; gap:10px; padding:8px 0; border-bottom:1px solid var(--line); }
        .pager { display:flex; gap:8px; justify-content:center; margin-top:20px; }
        pre { white-space:pre-wrap; font:inherit; }
        .sources { display:flex; border:1px solid var(--line); border-radius:6px; overflow:hidden; }
        .sources a { padding:7px 12px; color:var(--fg); text-decoration:none; }
        .sources a.on { background:var(--accent); color:#fff; }
        .slider { display:flex; gap:12px; overflow-x:auto; padding-bottom:8px; margin-bottom:16px; }
        .slider .card { flex:0 0 140px; }
        .subs form { display:flex; flex-wrap:wrap; gap:8px; align-items:center; margin-bottom:10px; }
        select, input[type=number] { padding:7px; border:1px solid var(--line); border-radius:6px; background:var(--bg); color:var(--fg); }
        input[type=number] { width:80px; }
        .tags { display:flex; flex-wrap:wrap; gap:6px; }
        .tags span { border:1px solid var(--line); border-radius:999px; padding:2px 10px; font-size:13px; }
        .people { display:grid; grid-template-columns:repeat(auto-fill, minmax(130px, 1fr)); gap:14px; }
    </style>
</head>
<body>
<header>
    <a class="brand" href="{{ route('home') }}">MovieFinder</a>
    @php $src = $src ?? session('source', config('movies.default_source')); @endphp
    <nav class="sources" aria-label="Source">
        @foreach (\App\Http\Controllers\CatalogController::SOURCES as $key => $label)
            <a href="{{ route('home', ['src' => $key]) }}" @class(['on' => $src === $key])>{{ $label }}</a>
        @endforeach
    </nav>
    <form class="search" action="{{ route('home') }}" method="get">
        <input type="hidden" name="src" value="{{ $src }}">
        <input type="search" name="q" value="{{ $q ?? '' }}" placeholder="Search titles" dir="auto">
        <button type="submit">Search</button>
    </form>
    @if ($src === 'db2')
        <a class="btn" href="{{ route('cast') }}">Actors</a>
    @endif
</header>
<main>
    @if (session('status')) <p class="status">{{ session('status') }}</p> @endif
    @if (session('error')) <p class="error">{{ session('error') }}</p> @endif
    @yield('content')
</main>
</body>
</html>

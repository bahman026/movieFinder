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
    </style>
</head>
<body>
<header>
    <a class="brand" href="{{ route('home') }}">MovieFinder</a>
    <form class="search" action="{{ route('home') }}" method="get">
        <input type="search" name="q" value="{{ $q ?? '' }}" placeholder="Search titles" dir="auto">
        <button type="submit">Search</button>
    </form>
    <form action="{{ route('reset') }}" method="post">
        @csrf
        <button type="submit" title="Drop the anonymous upstream session">New session</button>
    </form>
</header>
<main>
    @if (session('status')) <p class="status">{{ session('status') }}</p> @endif
    @if (session('error')) <p class="error">{{ session('error') }}</p> @endif
    @yield('content')
</main>
</body>
</html>

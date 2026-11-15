@extends('site.layout')

@section('title', 'Watch · '.($meta['title'] ?? ''))

@section('content')
    @php $back = route('title', array_filter([$src, $id, 'kind' => $src === 'db1' ? $meta['kind'] : null])); @endphp
    <p><a href="{{ $back }}">← {{ $meta['title'] }}</a></p>
    <h1 dir="auto" style="margin-bottom:4px">{{ $meta['title'] }}</h1>
    <p class="muted" dir="auto" style="margin-top:0">{{ $label }} · {{ $file }}</p>

    @unless ($browserPlayable)
        <p class="notice">This file is x265/HEVC, which most browsers can't play. If the picture stays black,
            use <strong>Download</strong> or open the <strong>Direct link</strong> in VLC or PotPlayer.</p>
    @endunless

    <section class="player">
        <video id="player" controls preload="metadata" playsinline crossorigin="anonymous">
            <source src="{{ $streamUrl }}">
            @if ($chosen)
                <track kind="subtitles" label="{{ $chosen['label'] }}" srclang="{{ $subForm['lang'] }}" default
                       src="{{ route('subtitles.vtt', ['provider' => $chosen['provider'], 'ref' => $chosen['ref'], 'lang' => $subForm['lang']]) }}">
            @endif
        </video>
        <p id="playError" class="error" hidden>The browser can't play this file (usually an MKV with x265 or an unsupported audio track).
            Use Download, or open the Direct link in VLC or PotPlayer.</p>

        <div class="bar">
            <a class="btn" href="{{ $downloadUrl }}" rel="nofollow">⬇ Download video</a>
            <a class="btn ghost" href="{{ $directUrl }}" rel="noreferrer nofollow" title="For VLC, PotPlayer or a download manager">Direct link</a>
            <button type="button" class="btn" id="copyLink" data-url="{{ $directUrl }}">Copy direct link</button>
            @if ($chosen)
                <span class="muted">Subtitle: <span dir="auto">{{ $chosen['label'] }}</span></span>
                <a class="btn ghost" href="{{ route('subtitles.download', ['provider' => $chosen['provider'], 'ref' => $chosen['ref']]) }}" rel="nofollow">⬇ Download subtitle</a>
            @endif
            <label class="btn" style="cursor:pointer">Use my own .srt / .vtt
                <input type="file" id="ownSub" accept=".srt,.vtt,text/vtt,application/x-subrip" hidden>
            </label>
        </div>
    </section>

    <section class="subs">
        <h3>Subtitle for this video</h3>
        <form action="{{ route('watch', [$src, $id, $index]) }}" method="get">
            <select name="subs" aria-label="Subtitle source">
                <option value="mysubs" @selected($subForm['provider'] === 'mysubs')>my-subs.co</option>
                <option value="opensubtitles" @selected($subForm['provider'] === 'opensubtitles')>OpenSubtitles</option>
            </select>
            <select name="lang" aria-label="Language">
                @foreach ($languages as $code => $name)
                    <option value="{{ $code }}" @selected($subForm['lang'] === $code)>{{ $name }}</option>
                @endforeach
            </select>
            @unless ($meta['is_movie'])
                <input type="number" name="season" min="1" placeholder="Season" value="{{ $subForm['season'] ?: '' }}" aria-label="Season">
                <input type="number" name="episode" min="1" placeholder="Episode" value="{{ $subForm['episode'] ?: '' }}" aria-label="Episode">
            @endunless
            <button type="submit">Find subtitles</button>
        </form>

        @if ($subError)
            <p class="error">{{ $subError }}</p>
        @elseif (is_array($subs))
            <ul class="links">
                @forelse ($subs as $sub)
                    @php $subLabel = trim(($sub['language'] ?: strtoupper($subForm['lang'])).' · '.($sub['release'] ?: $sub['title']), ' ·'); @endphp
                    <li>
                        <span><span dir="auto">{{ $sub['release'] ?: $sub['title'] }}</span>
                            <span class="muted">{{ $sub['language'] }}@if ($sub['downloads']) · {{ number_format($sub['downloads']) }} downloads @endif</span></span>
                        <span class="actions">
                            <a class="btn primary" href="{{ route('watch', [$src, $id, $index, 'subs' => $subForm['provider'], 'lang' => $subForm['lang'],
                                'season' => $subForm['season'] ?: null, 'episode' => $subForm['episode'] ?: null,
                                'sub' => $sub['provider'].':'.$sub['ref'], 'sublabel' => mb_substr($subLabel, 0, 80)]) }}">Use</a>
                            <a class="btn" href="{{ route('subtitles.download', ['provider' => $sub['provider'], 'ref' => $sub['ref']]) }}" rel="nofollow">Download</a>
                        </span>
                    </li>
                @empty
                    <li class="muted">No subtitles found in that language.</li>
                @endforelse
            </ul>
        @else
            <p class="muted">Pick a source and language, then choose <strong>Use</strong> to show a subtitle in the player.
                OpenSubtitles allows 5 downloads a day for the whole site; my-subs.co has no limit.</p>
        @endif
    </section>

    <script>
        (function () {
            var video = document.getElementById('player');
            video.addEventListener('error', function () { document.getElementById('playError').hidden = false; }, true);
            // A <source> error does not bubble to the video element; listen on it too.
            var source = video.querySelector('source');
            if (source) source.addEventListener('error', function () { document.getElementById('playError').hidden = false; });

            document.getElementById('copyLink').addEventListener('click', function () {
                var btn = this;
                navigator.clipboard.writeText(btn.dataset.url).then(function () { btn.textContent = 'Copied'; });
            });

            // A local subtitle file never leaves the browser: SRT is converted to WebVTT here.
            document.getElementById('ownSub').addEventListener('change', function () {
                var file = this.files[0];
                if (!file) return;
                var reader = new FileReader();
                reader.onload = function () {
                    var text = String(reader.result).replace(/^﻿/, '').replace(/\r\n?/g, '\n');
                    if (!/^WEBVTT/.test(text)) {
                        text = 'WEBVTT\n\n' + text.replace(/(\d{1,2}:\d{2}:\d{2}),(\d{1,3})/g, '$1.$2');
                    }
                    Array.prototype.forEach.call(video.querySelectorAll('track'), function (t) { t.remove(); });
                    for (var i = 0; i < video.textTracks.length; i++) video.textTracks[i].mode = 'disabled';
                    var track = document.createElement('track');
                    track.kind = 'subtitles';
                    track.label = file.name;
                    track.src = URL.createObjectURL(new Blob([text], { type: 'text/vtt' }));
                    track.default = true;
                    video.appendChild(track);
                    track.addEventListener('load', function () { track.track.mode = 'showing'; });
                    track.track.mode = 'showing';
                };
                // Persian SRTs are often Windows-1256; try UTF-8 first and fall back.
                var probe = new FileReader();
                probe.onload = function () {
                    var bytes = new Uint8Array(probe.result);
                    try { new TextDecoder('utf-8', { fatal: true }).decode(bytes); reader.readAsText(file, 'utf-8'); }
                    catch (e) { reader.readAsText(file, 'windows-1256'); }
                };
                probe.readAsArrayBuffer(file);
            });
        })();
    </script>
@endsection

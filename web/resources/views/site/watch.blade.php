@extends('site.layout')

@section('title', 'Watch · '.($meta['title'] ?? ''))

@section('content')
    @php $back = route('title', array_filter([$src, $id, 'kind' => $src === 'db1' ? $meta['kind'] : null])); @endphp
    <p><a href="{{ $back }}">← {{ $meta['title'] }}</a></p>
    <h1 dir="auto" style="margin-bottom:4px">{{ $meta['title'] }}</h1>
    <p class="muted" dir="auto" style="margin-top:0">{{ $label }} · {{ $file }}</p>

    @if ($formatWarning)
        <p class="notice">{{ $formatWarning }} If it doesn't play, use <strong>Open in VLC</strong> (phone) or
            <strong>Download</strong> / <strong>Copy link</strong> into VLC or PotPlayer.</p>
    @endif

    <section class="player">
        {{-- Straight from the database host: no traffic on this server. No crossorigin
             attribute: the hosts send no CORS headers, and the subtitle track is same-origin. --}}
        <video id="player" controls preload="metadata" playsinline data-direct="{{ $directUrl }}" data-via="{{ $streamUrl }}">
            <source src="{{ $directUrl }}">
            @if ($chosen)
                <track kind="subtitles" label="{{ $chosen['label'] }}" srclang="{{ $subForm['lang'] }}" default
                       src="{{ route('subtitles.vtt', ['provider' => $chosen['provider'], 'ref' => $chosen['ref'], 'lang' => $subForm['lang']]) }}">
            @endif
        </video>
        <p id="playError" class="error" hidden></p>
        <p id="dlNote" class="notice" hidden>Downloading the video and its subtitle. If only one file arrives, allow
            <strong>multiple downloads</strong> when the browser asks, or tap <strong>Download subtitle</strong>.
            Keep both files in the same folder so the player finds the subtitle.</p>

        <div class="bar">
            <a class="btn" id="dlVideo" href="{{ $directUrl }}" rel="noreferrer nofollow" title="Direct from the database host: no traffic on this server. If the browser warns, choose Keep.">⬇ Download video</a>
            {{-- Video straight from the movie host + the chosen subtitle saved under the video's
                 name (Movie.fa.srt), so players load it automatically. --}}
            <button type="button" class="btn primary" id="dlBoth" data-base="{{ $fileBase }}" @unless ($chosen) hidden @endunless
                    title="Downloads the video and the selected subtitle with matching names: VLC, MX Player and PotPlayer show the subtitle automatically">⬇ Video + subtitle</button>
            <button type="button" class="btn" id="copyLink" data-url="{{ $directUrl }}" title="For IDM, ADM, VLC or PotPlayer">Copy link</button>
            <button type="button" class="btn ghost" id="viaSite" title="Fallback if the direct stream fails: the video then goes through codegeek.ir and uses its traffic">Play via codegeek</button>
            <a class="btn ghost" href="{{ $renamedUrl }}" rel="nofollow" title="Saves the video as {{ $file }}. It goes through codegeek.ir and uses its traffic; the direct download keeps the host's name, which can't be changed">⬇ Renamed (via codegeek)</a>
            <a class="btn ghost" id="vlcLink" href="vlc://{{ $directUrl }}" data-url="{{ $directUrl }}" data-title="{{ $file }}" title="Opens the VLC app, streaming straight from the database host, with the selected subtitle (Android and iPhone)">Open in VLC</a>
            <span class="muted" id="curSubBox" @unless ($chosen) hidden @endunless>Subtitle: <span dir="auto" id="curSub">{{ $chosen['label'] ?? '' }}</span></span>
            <a class="btn ghost" id="curSubDl" rel="nofollow" @if ($chosen) href="{{ route('subtitles.download', ['provider' => $chosen['provider'], 'ref' => $chosen['ref'], 'lang' => $subForm['lang'], 'name' => $fileBase.'.'.$subForm['lang']]) }}" @else hidden @endif>⬇ Download subtitle</a>
            <button type="button" class="btn ghost" id="subOff" @unless ($chosen) hidden @endunless>Subtitle off</button>
            <label class="btn" style="cursor:pointer">Use my own .srt / .vtt
                <input type="file" id="ownSub" accept=".srt,.vtt,text/vtt,application/x-subrip" hidden>
            </label>
        </div>
    </section>

    <section class="subs">
        <h3>Subtitle for this video</h3>
        <form action="{{ route('watch', [$src, $id, $index]) }}" method="get" id="subSearch">
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

        <div id="subResults">
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
                            <a class="btn primary use-sub"
                               data-vtt="{{ route('subtitles.vtt', ['provider' => $sub['provider'], 'ref' => $sub['ref'], 'lang' => $subForm['lang']]) }}"
                               data-dl="{{ route('subtitles.download', ['provider' => $sub['provider'], 'ref' => $sub['ref']]) }}"
                               data-label="{{ mb_substr($subLabel, 0, 80) }}" data-lang="{{ $subForm['lang'] }}"
                               href="{{ route('watch', [$src, $id, $index, 'subs' => $subForm['provider'], 'lang' => $subForm['lang'],
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
        </div>
    </section>

    <script>
        (function () {
            var video = document.getElementById('player'), source = video.querySelector('source');
            var errBox = document.getElementById('playError'), viaBtn = document.getElementById('viaSite');
            var handled = false;

            function say(html, isError) {
                errBox.innerHTML = html;
                errBox.className = isError ? 'error' : 'notice';
                errBox.hidden = false;
            }

            // Switch the same player to the through-the-server stream, keeping the position.
            function playVia() {
                var at = video.currentTime || 0, wasPlaying = !video.paused;
                handled = false;
                source.src = video.dataset.via;
                video.load();
                video.addEventListener('loadedmetadata', function once() {
                    video.removeEventListener('loadedmetadata', once);
                    if (at) video.currentTime = at;
                    if (wasPlaying || at === 0) video.play().catch(function () {});
                });
                viaBtn.disabled = true; viaBtn.textContent = 'Playing via codegeek';
            }
            viaBtn.addEventListener('click', function () { errBox.hidden = true; playVia(); });

            // Is the movie host reachable from this device at all? An opaque no-cors
            // fetch resolves as soon as the host answers (then it is aborted, so the
            // file is not downloaded) and rejects when the network can't reach it.
            function hostReachable(url) {
                var ctrl = new AbortController(), timer = setTimeout(function () { ctrl.abort(); }, 8000);
                return fetch(url, { mode: 'no-cors', cache: 'no-store', signal: ctrl.signal })
                    .then(function () { return true; }, function () { return false; })
                    .then(function (ok) { clearTimeout(timer); ctrl.abort(); return ok; });
            }

            var formatHelp = ' Use <strong>Open in VLC</strong> on a phone, or <strong>Download</strong> / <strong>Copy link</strong> into VLC or PotPlayer.';
            function onFail() {
                if (handled) return;
                handled = true;
                if (source.src === video.dataset.via) {
                    say('This browser can\'t play this file (usually x265 video or AC3/DTS audio).' + formatHelp, true);
                    return;
                }
                say('The video didn\'t start. Checking whether this device can reach the movie host…', false);
                hostReachable(video.dataset.direct).then(function (reachable) {
                    if (reachable) {
                        // Same bytes via codegeek would fail the same way: don't spend server traffic on it.
                        say('The movie host answers, so this browser can\'t play the file\'s format (usually x265 video or AC3/DTS audio).' + formatHelp, true);
                    } else {
                        say('This device can\'t reach the movie host (often a VPN or an internet-provider block), so it is now <strong>playing via codegeek</strong>, which uses this server\'s traffic. Turning the VPN off may let it play directly.', false);
                        playVia();
                    }
                });
            }
            video.addEventListener('error', onFail, true);
            // A <source> error does not bubble to the video element; listen on it too.
            source.addEventListener('error', onFail);

            document.getElementById('copyLink').addEventListener('click', function () {
                var btn = this;
                navigator.clipboard.writeText(btn.dataset.url).then(function () { btn.textContent = 'Copied'; });
            });

            var curBox = document.getElementById('curSubBox'), cur = document.getElementById('curSub'),
                curDl = document.getElementById('curSubDl'), off = document.getElementById('subOff');

            // Swap the subtitle on the playing video. Only <track> elements change,
            // so playback continues from the same second, without a reload.
            function setTrack(src, label, lang, dlUrl) {
                Array.prototype.forEach.call(video.querySelectorAll('track'), function (t) { t.remove(); });
                for (var i = 0; i < video.textTracks.length; i++) video.textTracks[i].mode = 'disabled';
                if (src) {
                    var track = document.createElement('track');
                    track.kind = 'subtitles';
                    track.label = label;
                    track.srclang = lang || '';
                    track.src = src;
                    track.default = true;
                    video.appendChild(track);
                    track.track.mode = 'showing';
                    track.addEventListener('load', function () { track.track.mode = 'showing'; });
                    track.addEventListener('error', function () { cur.textContent = label + ' (could not be loaded)'; });
                }
                cur.textContent = label || '';
                curBox.hidden = off.hidden = !src;
                if (dlUrl) {
                    // Name the subtitle after the video (Movie.2010.720p.fa.srt) so players pick it up.
                    curDl.href = dlUrl + '&lang=' + encodeURIComponent(lang || '') + '&name=' + encodeURIComponent(dlBoth.dataset.base + '.' + (lang || 'sub'));
                    curDl.hidden = false;
                } else { curDl.hidden = true; curDl.removeAttribute('href'); }
                dlBoth.hidden = !dlUrl;
                updateVlc(dlUrl ? curDl.href : null);
            }
            off.addEventListener('click', function () { setTrack(null); });

            // Video + subtitle: the video straight from the movie host (no server traffic),
            // then the subtitle under the video's name. Two separate downloads; browsers may
            // ask once to allow multiple downloads from this site.
            var dlBoth = document.getElementById('dlBoth');

            // Open in VLC with the selected subtitle. Android: an intent with VLC's
            // subtitles_location extra; iPhone/iPad: VLC's x-callback "sub" parameter.
            // Desktop browsers only know vlc://, which carries no subtitle.
            var vlc = document.getElementById('vlcLink'), ua = navigator.userAgent;
            var isAndroid = /Android/i.test(ua), isIOS = /iPhone|iPad|iPod/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
            function updateVlc(subUrl) {
                var url = vlc.dataset.url, e = encodeURIComponent;
                if (isAndroid) {
                    var m = url.match(/^(https?):\/\/(.*)$/);
                    vlc.href = 'intent://' + m[2] + '#Intent;scheme=' + m[1] + ';type=video/*;package=org.videolan.vlc;'
                        + 'S.title=' + e(vlc.dataset.title) + ';' + (subUrl ? 'S.subtitles_location=' + e(subUrl) + ';' : '') + 'end';
                } else if (isIOS) {
                    vlc.href = 'vlc-x-callback://x-callback-url/stream?url=' + e(url) + (subUrl ? '&sub=' + e(subUrl) : '');
                } else {
                    vlc.href = 'vlc://' + url;
                }
            }
            updateVlc(curDl.hasAttribute('href') ? curDl.href : null);
            function clickLink(href) {
                var a = document.createElement('a');
                a.href = href; a.rel = 'noreferrer'; a.style.display = 'none';
                document.body.appendChild(a); a.click(); a.remove();
            }
            dlBoth.addEventListener('click', function () {
                if (!curDl.href) return;
                document.getElementById('dlNote').hidden = false;
                clickLink(document.getElementById('dlVideo').href);
                setTimeout(function () { clickLink(curDl.href); }, 1200);
            });

            document.addEventListener('click', function (e) {
                var a = e.target.closest && e.target.closest('a.use-sub');
                if (!a || e.ctrlKey || e.metaKey || e.shiftKey) return;
                e.preventDefault();
                setTrack(a.dataset.vtt, a.dataset.label, a.dataset.lang, a.dataset.dl);
                history.replaceState(null, '', a.href); // a reload or a shared link keeps the choice
            });

            // Search without leaving the page: fetch the same page and swap in its results.
            var form = document.getElementById('subSearch'), results = document.getElementById('subResults');
            form.addEventListener('submit', function (e) {
                e.preventDefault();
                var url = form.action + '?' + new URLSearchParams(new FormData(form)).toString();
                var btn = form.querySelector('button[type=submit]');
                btn.disabled = true; btn.textContent = 'Searching…';
                fetch(url, { credentials: 'same-origin' })
                    .then(function (r) { return r.text(); })
                    .then(function (html) {
                        var doc = new DOMParser().parseFromString(html, 'text/html');
                        var fresh = doc.getElementById('subResults');
                        results.innerHTML = fresh ? fresh.innerHTML : '<p class="error">Search failed. Try again.</p>';
                        history.replaceState(null, '', url);
                    })
                    .catch(function () { results.innerHTML = '<p class="error">Search failed. Check your connection and try again.</p>'; })
                    .then(function () { btn.disabled = false; btn.textContent = 'Find subtitles'; });
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
                    setTrack(URL.createObjectURL(new Blob([text], { type: 'text/vtt' })), file.name, '', null);
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

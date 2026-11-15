<?php

namespace App\Http\Controllers;

use App\Support\StreamToken;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\StreamedResponse;

/**
 * Passes a video through to the browser. The file hosts are http-only, which an
 * https page cannot play or download without mixed-content blocking, so the
 * bytes come through this site instead.
 *
 * The browser's own Range header is forwarded and the upstream answer copied
 * through chunk by chunk, so seeking works and nothing is buffered: memory
 * stays flat whatever the file size. The transfer runs in two phases on one
 * curl handle: first until the upstream headers are in (so the response gets
 * the real status, length and range), then the body inside the streamed
 * response. When the viewer goes away the upstream transfer is cancelled.
 * ?dl=1 serves the same bytes as an attachment.
 */
class StreamController extends Controller
{
    private const PASS_HEADERS = ['content-length', 'content-range', 'accept-ranges', 'last-modified', 'etag'];

    public function stream(Request $request)
    {
        $token = StreamToken::read((string) $request->query('t'));
        abort_if(! $token, 404);

        $download = $request->boolean('dl');
        $name = preg_replace('/[^\w.\- ]+/u', '_', $token['n']) ?: 'video';
        $range = $request->header('Range');

        $status = 0;
        $headers = [];
        $headersDone = false;
        $streaming = false;
        $pending = '';

        $ch = curl_init($token['u']);
        curl_setopt_array($ch, [
            CURLOPT_FOLLOWLOCATION => true,
            CURLOPT_MAXREDIRS => 5,
            CURLOPT_CONNECTTIMEOUT => 20,
            CURLOPT_LOW_SPEED_LIMIT => 1,   // give up on a transfer that stalls
            CURLOPT_LOW_SPEED_TIME => 60,   // for a full minute
            CURLOPT_USERAGENT => 'Mozilla/5.0',
            CURLOPT_NOBODY => $request->isMethod('HEAD'),
            CURLOPT_HTTPHEADER => $range ? ['Range: '.$range] : [],
            // One header block arrives per redirect hop; only the final one counts.
            CURLOPT_HEADERFUNCTION => function ($ch, string $line) use (&$status, &$headers, &$headersDone) {
                $trim = trim($line);
                if (preg_match('#^HTTP/\S+\s+(\d{3})#', $trim, $m)) {
                    [$status, $headers] = [(int) $m[1], []];
                } elseif ($trim !== '' && str_contains($trim, ':')) {
                    [$k, $v] = array_map('trim', explode(':', $trim, 2));
                    if (in_array(strtolower($k), self::PASS_HEADERS, true)) {
                        $headers[$k] = $v;
                    }
                } elseif ($trim === '' && $status && ($status < 300 || $status >= 400)) {
                    $headersDone = true;
                }

                return strlen($line);
            },
            CURLOPT_WRITEFUNCTION => function ($ch, string $chunk) use (&$streaming, &$pending) {
                if (! $streaming) {
                    $pending .= $chunk; // arrived before the response was handed back

                    return strlen($chunk);
                }
                echo $chunk;
                flush();

                return connection_aborted() ? -1 : strlen($chunk); // viewer left: stop upstream
            },
        ]);

        $mh = curl_multi_init();
        curl_multi_add_handle($mh, $ch);
        do {
            curl_multi_exec($mh, $running);
            if ($headersDone) {
                break;
            }
            curl_multi_select($mh, 1.0);
        } while ($running);

        if (! $headersDone) {
            curl_multi_remove_handle($mh, $ch);
            curl_close($ch);
            curl_multi_close($mh);

            return response('The file server did not answer.', 502, ['Content-Type' => 'text/plain']);
        }

        $out = $headers + [
            'Content-Type' => self::contentType($name, $download),
            'X-Content-Type-Options' => 'nosniff',
            'Cache-Control' => 'private, no-store',
            'X-Accel-Buffering' => 'no',
        ];
        if ($download) {
            $out['Content-Disposition'] = 'attachment; filename="'.$name.'"';
        }

        return new StreamedResponse(function () use ($mh, $ch, &$streaming, &$pending) {
            @set_time_limit(0);
            ignore_user_abort(false);
            echo $pending;
            $pending = '';
            flush();
            $streaming = true;

            do {
                curl_multi_exec($mh, $running);
                if ($running) {
                    curl_multi_select($mh, 1.0);
                }
            } while ($running && ! connection_aborted());

            curl_multi_remove_handle($mh, $ch);
            curl_close($ch);
            curl_multi_close($mh);
        }, $status, $out);
    }

    /**
     * The hosts send application/octet-stream. Browsers play an MKV holding
     * H.264/AAC when it is labelled WebM (the same Matroska container); x265/HEVC
     * MKVs usually will not play in a browser at all, whatever the label.
     */
    private static function contentType(string $name, bool $download): string
    {
        if ($download) {
            return 'application/octet-stream';
        }

        return match (strtolower(pathinfo($name, PATHINFO_EXTENSION))) {
            'mp4', 'm4v' => 'video/mp4',
            'mkv', 'webm' => 'video/webm',
            default => 'application/octet-stream',
        };
    }
}

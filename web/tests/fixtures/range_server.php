<?php
// Router for `php -S` in StreamTest: serves 1000 known bytes with Range support,
// and /redirect -> /file.mp4 like the real file hosts do.
if ($_SERVER['REQUEST_URI'] === '/redirect') {
    header('Location: /file.mp4', true, 302);
    return true;
}
$body = str_repeat('0123456789', 100);
header('Accept-Ranges: bytes');
header('Content-Type: application/octet-stream');
if (preg_match('/bytes=(\d+)-(\d*)/', $_SERVER['HTTP_RANGE'] ?? '', $m)) {
    $end = $m[2] === '' ? 999 : (int) $m[2];
    http_response_code(206);
    header("Content-Range: bytes {$m[1]}-$end/1000");
    $body = substr($body, (int) $m[1], $end - (int) $m[1] + 1);
}
header('Content-Length: '.strlen($body));
echo $body;

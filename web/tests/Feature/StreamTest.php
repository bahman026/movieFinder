<?php

namespace Tests\Feature;

use App\Support\StreamToken;
use App\Support\Vtt;
use Tests\TestCase;

class StreamTest extends TestCase
{
    private static $server;

    private static int $port;

    public static function setUpBeforeClass(): void
    {
        self::$port = random_int(20000, 40000);
        self::$server = proc_open(
            [PHP_BINARY, '-S', '127.0.0.1:'.self::$port, __DIR__.'/../fixtures/range_server.php'],
            [['pipe', 'r'], ['file', '/dev/null', 'w'], ['file', '/dev/null', 'w']], $pipes
        );
        for ($i = 0; $i < 50 && ! @fsockopen('127.0.0.1', self::$port); $i++) {
            usleep(100_000);
        }
    }

    public static function tearDownAfterClass(): void
    {
        proc_terminate(self::$server);
    }

    private function url(string $path): string
    {
        return 'http://127.0.0.1:'.self::$port.$path;
    }

    public function test_range_is_forwarded_and_206_passed_through_after_redirects(): void
    {
        $t = StreamToken::make($this->url('/redirect'), 'Movie.2010.mp4');

        $r = $this->get('/stream?t='.urlencode($t), ['Range' => 'bytes=10-19']);

        $r->assertStatus(206)->assertHeader('Content-Range', 'bytes 10-19/1000')->assertHeader('Content-Type', 'video/mp4');
        $this->assertSame('0123456789', $r->streamedContent());
    }

    public function test_download_is_an_attachment_with_the_file_name(): void
    {
        $t = StreamToken::make($this->url('/file.mp4'), 'Movie "x".mkv');
        $r = $this->get('/stream?dl=1&t='.urlencode($t));

        $r->assertOk()->assertHeader('Content-Disposition', 'attachment; filename="Movie _x_.mkv"');
        $this->assertSame(1000, strlen($r->streamedContent()));
    }

    public function test_stream_needs_a_valid_unexpired_token(): void
    {
        $this->get('/stream?t=garbage')->assertNotFound();
        $this->get('/stream?t='.urlencode(encrypt(['u' => $this->url('/file.mp4'), 'n' => 'x', 'e' => time() - 1], true)))->assertNotFound();
        $this->get('/stream?t='.urlencode(StreamToken::make('file:///etc/passwd', 'x')))->assertNotFound();
    }

    public function test_srt_becomes_utf8_webvtt_including_windows_1256(): void
    {
        $srt = "1\r\n00:00:01,500 --> 00:00:02,000\r\n\xD3\xE1\xC7\xE3\r\n";
        $vtt = Vtt::fromSubtitle($srt, 'fa');

        $this->assertStringStartsWith("WEBVTT\n\n", $vtt);
        $this->assertStringContainsString('00:00:01.500 --> 00:00:02.000', $vtt);
        $this->assertStringContainsString('سلام', $vtt);

        $this->expectException(\RuntimeException::class);
        Vtt::fromSubtitle('[Script Info] ass file', 'en');
    }
}

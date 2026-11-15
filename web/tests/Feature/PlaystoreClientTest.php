<?php

namespace Tests\Feature;

use App\Services\Playstore\PlaystoreClient;
use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Http;
use RuntimeException;
use Tests\TestCase;

class PlaystoreClientTest extends TestCase
{
    private function client(): PlaystoreClient
    {
        return new PlaystoreClient(['hosts' => 'http://a.test, http://b.test', 'base_path' => '/playstore/api',
            'secret_key' => 'k', 'version' => '1', 'country' => 'other', 'sp' => true, 'timeout' => 5]);
    }

    protected function setUp(): void
    {
        parent::setUp();
        Cache::flush();
    }

    public function test_fails_over_to_next_host_and_pins_it(): void
    {
        $hits = [];
        Http::fake(function (Request $r) use (&$hits) {
            $hits[] = parse_url($r->url(), PHP_URL_HOST);

            return str_contains($r->url(), 'a.test')
                ? Http::response('down', 503)
                : Http::response([['videos_id' => '1', 'title' => 'T', 'is_tvseries' => '1', 'release' => '2016-12-16']]);
        });

        $items = $this->client()->movies();
        $this->client()->movies();

        $this->assertSame(['a.test', 'b.test', 'b.test'], $hits); // dead mirror costs one request
        $this->assertSame('tvseries', $items[0]['kind']);
        $this->assertSame('2016', $items[0]['year']);
    }

    public function test_error_envelope_with_http_200_is_an_error(): void
    {
        Http::fake(['*' => Http::response(['status' => 'error', 'message' => 'bad key'])]);
        $this->expectExceptionMessage('bad key');
        $this->client()->search('x');
    }

    public function test_client_error_does_not_walk_the_mirrors(): void
    {
        $n = 0;
        Http::fake(function () use (&$n) {
            $n++;

            return Http::response('nope', 404);
        });
        try {
            $this->client()->movies();
            $this->fail('expected an error');
        } catch (RuntimeException) {
        }
        $this->assertSame(1, $n);
    }

    public function test_details_rewrites_dead_cdn_images_and_reads_sizes_in_mb(): void
    {
        Http::fake(['*' => Http::response([
            'videos_id' => '5', 'title' => 'Film', 'writer' => 'فیلم', 'imdbid' => 'tt123', 'release' => '2020',
            'poster_url' => 'http://dead.cdn.cf/playstore/uploads/p.jpg',
            'download_links' => [['label' => '720P', 'resolution' => '⇩', 'file_size' => '1536', 'download_url' => 'http://dl/x.mkv']],
        ])]);

        $d = $this->client()->details('movie', '5');

        $this->assertSame('http://a.test/playstore/uploads/p.jpg', $d['poster']);
        $this->assertSame('720P', $d['links'][0]['name']);
        $this->assertSame('1.5 GB', $d['links'][0]['size']);
        $this->assertSame('فیلم', $d['local_title']);
    }
}

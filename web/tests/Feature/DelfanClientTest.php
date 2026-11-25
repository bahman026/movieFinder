<?php

namespace Tests\Feature;

use App\Services\Delfan\DelfanClient;
use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\Http;
use Tests\TestCase;

class DelfanClientTest extends TestCase
{
    public function test_search_threads_the_rolling_nonce_and_uses_a_random_device_id(): void
    {
        $sent = [];
        Http::fake(function (Request $r) use (&$sent) {
            $sent[] = $r;
            if (str_contains($r->url(), 'action=login')) {
                return Http::response(['night_mode' => 'n', 'q1' => '1', 'q2' => '2', 'infos' => [['auth' => 'AUTH']]]);
            }
            if (str_contains($r->url(), 'action=vitrin')) {
                return Http::response(['q1' => 10, 'q2' => 20]);
            }

            return Http::response(['q1' => '30', 'q2' => '40', 'all' => [['videos_id' => '7', 'title' => 'X']]]);
        });

        $client = new DelfanClient($this->app['session.store'], config('delfan'));
        $items = $client->search('x');

        $this->assertSame('7', $items[0]['id']);
        $this->assertCount(3, $sent); // login, vitrin seed, search

        // The search must be signed with the vitrin reply's q1/q2.
        $this->assertSame(md5((string) (10 + 20 + 101)), $sent[2]->data()['an']);
        $this->assertStringContainsString('AUTH', $sent[2]->data()['body']);

        // No hardcoded device id from the desktop client.
        $androidId = $sent[0]->data()['android_id'];
        $this->assertMatchesRegularExpression('/^[0-9a-f]{16}$/', $androidId);
        $this->assertNotSame('95bf8f966958ca70', $androidId);
    }

    public function test_resolve_only_follows_links_the_title_offered(): void
    {
        $this->get('/title/db2/1/link/0')->assertNotFound();
    }

    public function test_home_rows_keep_the_groups_and_rank_top_by_imdb(): void
    {
        Http::fake(function (Request $r) {
            if (str_contains($r->url(), 'action=login')) {
                return Http::response(['q1' => '1', 'q2' => '2', 'infos' => [['auth' => 'AUTH']]]);
            }

            return Http::response(['q1' => 10, 'q2' => 20,
                'NewMovie' => [['videos_id' => '1', 'title' => 'A', 'imdb' => '6.1'], ['videos_id' => '2', 'title' => 'B', 'imdb' => '8.4']],
                'NewSerie' => [['videos_id' => '3', 'title' => 'C', 'imdb' => '7.0'], ['videos_id' => '2', 'title' => 'B', 'imdb' => '8.4']],
                'vige' => [['videos_id' => '4', 'title' => 'D', 'imdb' => '']],
            ]);
        });

        $rows = (new DelfanClient($this->app['session.store'], config('delfan')))->homeRows();

        $this->assertSame(['1', '2'], array_column($rows['new_movies'], 'id'));
        $this->assertSame(['3', '2'], array_column($rows['new_series'], 'id'));
        $this->assertSame(['2', '3', '1'], array_column($rows['top'], 'id')); // unrated and duplicates left out
    }
}

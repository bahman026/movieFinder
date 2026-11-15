<?php

namespace Tests\Feature;

use App\Support\WatchUrl;
use Tests\TestCase;

class WatchOverHttpTest extends TestCase
{
    public function test_watch_links_and_https_requests_move_to_http_when_enabled(): void
    {
        config(['movies.watch_over_http' => true, 'app.url' => 'https://codegeek.ir']);
        \URL::forceRootUrl('https://codegeek.ir');

        $this->assertStringStartsWith('http://codegeek.ir/watch/db2/1/0', WatchUrl::to('db2', '1', 0));
        $this->get('https://codegeek.ir/watch/db2/1/0?sub=x')->assertRedirect('http://codegeek.ir/watch/db2/1/0?sub=x');
    }

    public function test_disabled_by_default(): void
    {
        $this->assertFalse(config('movies.watch_over_http'));
        // An https request is not bounced to http: without session data it goes back to the title page instead.
        $this->get('https://localhost/watch/db2/1/0')->assertRedirect('https://localhost/title/db2/1');
    }
}

<?php

namespace Tests\Feature;

use App\Support\Img;
use Illuminate\Support\Facades\Http;
use Tests\TestCase;

class RateLimitTest extends TestCase
{
    public function test_a_page_full_of_images_does_not_use_up_the_page_limit(): void
    {
        Http::fake(['*' => Http::response('PNG', 200, ['Content-Type' => 'image/png'])]);

        for ($i = 0; $i < 80; $i++) {
            $this->get(Img::url("http://upstream.test/$i.png"))->assertOk();
        }
        // Pages and downloads have their own counters, untouched by the images.
        $this->get('/title/db2/1/link/0')->assertNotFound();
        $this->get('/up')->assertOk();
    }
}

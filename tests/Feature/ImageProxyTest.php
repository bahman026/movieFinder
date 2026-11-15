<?php

namespace Tests\Feature;

use App\Support\Img;
use Illuminate\Support\Facades\Http;
use Tests\TestCase;

class ImageProxyTest extends TestCase
{
    public function test_serves_images_this_site_linked_and_nothing_else(): void
    {
        Http::fake(['*' => Http::response('PNGDATA', 200, ['Content-Type' => 'image/png'])]);

        $this->get(Img::url('http://upstream.test/a.png'))->assertOk()->assertHeader('Content-Type', 'image/png');
        $this->get('/img?u=http://169.254.169.254/')->assertNotFound();
        $this->assertSame('', Img::url('file:///etc/passwd'));
    }
}

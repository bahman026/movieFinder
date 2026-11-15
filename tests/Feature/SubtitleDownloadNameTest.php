<?php

namespace Tests\Feature;

use Illuminate\Support\Facades\Cache;
use Tests\TestCase;

class SubtitleDownloadNameTest extends TestCase
{
    public function test_subtitle_is_saved_under_the_video_name_as_utf8_with_bom(): void
    {
        // A Persian subtitle in Windows-1256, as my-subs.co usually serves it (pre-cached: no network).
        $srt = "1\r\n00:00:01,000 --> 00:00:02,000\r\n\xD3\xE1\xC7\xE3\r\n";
        Cache::put('sub:'.sha1('mysubs|/downloads/x'), [base64_encode($srt), 'Some.Upload.srt'], 60);

        $r = $this->get('/subtitles/download?provider=mysubs&ref=/downloads/x&lang=fa&name=Inception.2010.720p.YIFY.fa');

        $r->assertOk()->assertHeader('Content-Disposition', 'attachment; filename="Inception.2010.720p.YIFY.fa.srt"');
        $this->assertStringStartsWith("\xEF\xBB\xBF1", $r->getContent());
        $this->assertStringContainsString('سلام', $r->getContent());
    }

    public function test_without_a_name_the_original_file_is_unchanged(): void
    {
        Cache::put('sub:'.sha1('mysubs|/downloads/y'), [base64_encode("raw\xD3"), 'Orig.srt'], 60);

        $r = $this->get('/subtitles/download?provider=mysubs&ref=/downloads/y');

        $r->assertOk()->assertHeader('Content-Disposition', 'attachment; filename="Orig.srt"');
        $this->assertSame("raw\xD3", $r->getContent());
    }
}

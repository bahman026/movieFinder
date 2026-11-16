<?php

namespace Tests\Feature;

use App\Support\FileName;
use Tests\TestCase;

class FileNameTest extends TestCase
{
    public function test_branding_is_removed_from_file_names(): void
    {
        $this->assertSame('Spider-Man.2026.480p.WEB-DL.SoftSub.mkv', FileName::clean('Spider-Man.2026.480p.WEB-DL.SoftSub.MovieJavan.mkv'));
        $this->assertSame('Inception.2010.720p.mkv', FileName::clean('Inception.2010.720p.[MovieJavan.com].mkv'));
        $this->assertSame('Show.S01E02.mkv', FileName::clean('Show.S01E02.Movie_Javan.mkv'));
        $this->assertSame('Plain.2020.mp4', FileName::clean('Plain.2020.mp4'));
        $this->assertSame('video.mkv', FileName::clean('MovieJavan.mkv'));
    }
}

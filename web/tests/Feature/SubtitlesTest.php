<?php

namespace Tests\Feature;

use App\Services\Subtitles\MySubsClient;
use App\Services\Subtitles\OpenSubtitlesClient;
use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\Http;
use Tests\TestCase;

class SubtitlesTest extends TestCase
{
    public function test_mysubs_episode_url_puts_episode_before_season_and_skips_spinoffs(): void
    {
        $asked = [];
        Http::fake(function (Request $r) use (&$asked) {
            $asked[] = parse_url($r->url(), PHP_URL_PATH);
            if (str_contains($r->url(), 'search.php')) {
                return Http::response('<a href="/showlistsubtitles-9-breaking-bad-minisodes">Breaking Bad Minisodes (2009)</a>'
                    .'<a href="/showlistsubtitles-7-breaking-bad">Breaking Bad (2008)</a>');
            }

            return Http::response("<h1>Breaking Bad Season 2 Episode 3 Subtitles</h1><div class='version'><i>WEB-DL</i>"
                ."<div class='lang'><i>Farsi/Persian</i> <b>Downloads :</b> 12 <a href='/downloads/abc'>x</a></div>"
                ."<div class='lang'><i>English</i> <b>Downloads :</b> 40 <a href='/downloads/def'>x</a></div></div>");
        });

        $subs = (new MySubsClient('https://my-subs.test'))->search('Breaking Bad', '2008', 'fa', 2, 3);

        $this->assertSame('/versions-7-3-2-breaking-bad-subtitles', $asked[1]);
        $this->assertCount(1, $subs);
        $this->assertSame('/downloads/abc', $subs[0]['ref']);
    }

    public function test_mysubs_download_passes_the_gate_and_only_accepts_gate_paths(): void
    {
        Http::fake(function (Request $r) {
            if (str_contains($r->url(), '/downloads/')) {
                return Http::response('<script>var REAL_URL = "\/download\/film-1.srt";</script>', 200, ['Set-Cookie' => 'PHPSESSID=s; path=/']);
            }

            return str_contains($r->header('Cookie')[0] ?? '', 'PHPSESSID=s')
                ? Http::response('1\n00:00:01,000 --> 00:00:02,000\nHi', 200, ['Content-Type' => 'application/x-subrip'])
                : Http::response('', 302);
        });

        $client = new MySubsClient('https://my-subs.test');
        [$data, $name] = $client->download('/downloads/tok');
        $this->assertStringContainsString('Hi', $data);
        $this->assertSame('film-1.srt', $name);

        $this->expectExceptionMessage('Not a my-subs.co download link');
        $client->download('http://169.254.169.254/latest');
    }

    public function test_opensubtitles_free_text_fallback_drops_other_titles(): void
    {
        Http::fake(function (Request $r) {
            if (str_contains($r->url(), 'imdb_id')) {
                return Http::response(['data' => []]);
            }
            $row = fn ($name, $year, $id) => ['id' => (string) $id, 'attributes' => ['language' => 'en', 'release' => $name,
                'feature_details' => ['movie_name' => "$year - $name", 'title' => $name, 'year' => $year], 'files' => [['file_id' => $id]]]];

            return Http::response(['data' => [$row('Heat', 1995, 1), $row('Heat Wave', 2022, 2), $row('Heat', 1972, 3)]]);
        });

        $subs = (new OpenSubtitlesClient('key'))->search('Heat', 'tt113277', '1995', 'en');

        $this->assertSame(['1'], array_column($subs, 'ref'));
        $this->assertTrue(\App\Services\Subtitles\TitleMatch::same('2010 - Inception', 'Inception'));
    }

    public function test_opensubtitles_without_key_says_where_to_get_one(): void
    {
        $this->expectExceptionMessage(OpenSubtitlesClient::REGISTER_URL);
        (new OpenSubtitlesClient(''))->search('x', '', '', 'en');
    }
}

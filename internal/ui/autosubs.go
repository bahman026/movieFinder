package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"moviefinder/internal/download"
	"moviefinder/internal/mux"
)

// Automatic subtitles for queued downloads.
//
// When a queued download finishes, the app searches subtitles in each of the
// configured languages (English and Persian by default), picks the one whose
// release name best matches the downloaded file, and embeds them into the
// video as soft tracks with ffmpeg. No ffmpeg means the subtitles are left
// beside the video as Movie.en.srt / Movie.fa.srt, which every player picks up
// on its own — the feature degrades, it does not fail.
//
// This runs only for the queue. A download-while-playing file is open in the
// player when it completes, and Windows will not let it be replaced under it.

// autoSubJob is what the subtitle search needs about a queued download,
// captured when it is queued: by the time it finishes the user has long moved
// on to another title, so u.subTitle no longer describes it.
type autoSubJob struct {
	title, imdb, year string
	season, episode   int
	series            bool
}

// namedSearch is one subtitle source, named for error messages.
type namedSearch struct {
	name string
	run  subtitleSearch
}

// episodePattern finds S01E02-style markers in a release name.
var episodePattern = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})\b`)

// episodeFrom reads season and episode from the first text that carries them.
func episodeFrom(texts ...string) (season, episode int) {
	for _, t := range texts {
		if m := episodePattern.FindStringSubmatch(t); m != nil {
			s, _ := strconv.Atoi(m[1])
			e, _ := strconv.Atoi(m[2])
			return s, e
		}
	}
	return 0, 0
}

// rememberAutoSub records a queued job's metadata. Called on the UI thread
// right after Queue.Add; onDone hops onto the UI thread too, so it always runs
// after this.
func (u *UI) rememberAutoSub(id int, meta autoSubJob) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.autoSubJobs == nil {
		u.autoSubJobs = map[int]autoSubJob{}
	}
	u.autoSubJobs[id] = meta
}

// setPostStatus is the line shown under a finished job while (and after) its
// subtitles are processed. Empty clears it.
func (u *UI) setPostStatus(id int, text string) {
	u.mu.Lock()
	if u.postStatus == nil {
		u.postStatus = map[int]string{}
	}
	u.postStatus[id] = text
	u.mu.Unlock()
	u.refreshDownloads()
}

func (u *UI) postStatusOf(id int) string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.postStatus[id]
}

// onDownloadDone is the queue's completion hook, on the UI thread.
func (u *UI) onDownloadDone(job download.Snapshot) {
	u.mu.Lock()
	meta, ok := u.autoSubJobs[job.ID]
	delete(u.autoSubJobs, job.ID)
	u.mu.Unlock()

	if !ok || !u.cfg.AutoSubtitles || strings.TrimSpace(meta.title) == "" {
		return
	}
	if meta.series && (meta.season == 0 || meta.episode == 0) {
		u.setPostStatus(job.ID, "No subtitles added: could not tell the season and episode from the file name.")
		return
	}

	// Resolved here, on the UI thread, for the reason subtitleSearcher gives.
	primary := u.cfg.SubtitleProvider
	secondary := providerOpenSubtitles
	if primary == providerOpenSubtitles {
		secondary = providerMySubs
	}
	searches := []namedSearch{
		{sourceLabel(primary), u.subtitleSearcher(primary)},
		{sourceLabel(secondary), u.subtitleSearcher(secondary)},
	}
	langs := append([]string(nil), u.cfg.AutoSubtitleLanguages...)
	ffmpegPath := u.cfg.FFmpegPath

	u.setPostStatus(job.ID, "Finding subtitles…")
	u.bg("Adding subtitles", func() {
		report := func(text string) { u.onUI("Adding subtitles", func() { u.setPostStatus(job.ID, text) }) }

		var tracks []mux.Track
		var sidecars, found, missing []string
		for _, lang := range langs {
			q := subtitleQuery{
				title: meta.title, imdbID: meta.imdb, year: meta.year, lang: lang,
				season: meta.season, episode: meta.episode,
			}
			data, name, err := fetchBestSubtitle(searches, q, filepath.Base(job.SavePath))
			if err != nil {
				missing = append(missing, languageLabel(lang))
				continue
			}
			path, err := writeLanguageSidecar(job.SavePath, lang, name, mux.ToUTF8(data, lang))
			if err != nil {
				missing = append(missing, languageLabel(lang))
				continue
			}
			sidecars = append(sidecars, path)
			found = append(found, languageLabel(lang))
			tracks = append(tracks, mux.Track{Path: path, Language: mux.ISO6392(lang), Title: languageLabel(lang)})
		}

		if len(tracks) == 0 {
			report("No subtitles found in " + strings.Join(missing, " or ") + ".")
			return
		}
		missingNote := ""
		if len(missing) > 0 {
			missingNote = " None found in " + strings.Join(missing, ", ") + "."
		}

		ffmpeg, err := mux.Detect(ffmpegPath)
		if err != nil {
			report("Saved " + strings.Join(found, ", ") + " subtitles beside the video (" + firstLine(err.Error()) + ")." + missingNote)
			return
		}

		report("Adding " + strings.Join(found, ", ") + " subtitles to the file…")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		out, err := mux.Embed(ctx, ffmpeg, job.SavePath, tracks)
		if err != nil {
			report("Saved " + strings.Join(found, ", ") + " subtitles beside the video; embedding failed: " + firstLine(err.Error()) + missingNote)
			return
		}
		// Embedded now; sidecars as well would show every track twice.
		for _, p := range sidecars {
			os.Remove(p)
		}
		report("Added " + strings.Join(found, ", ") + " subtitles — " + filepath.Base(out) + "." + missingNote)
	})
}

// fetchBestSubtitle searches each source in turn and downloads the best match.
// A source that errors or has nothing falls through to the next — the first
// being down (a 522 from my-subs.co, say) must not cost the subtitle.
func fetchBestSubtitle(searches []namedSearch, q subtitleQuery, videoName string) ([]byte, string, error) {
	var lastErr error
	for _, s := range searches {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		hits, err := s.run(ctx, q)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", s.name, err)
			continue
		}
		// Try the few best-ranked: a single upload can be broken or gated.
		for i, hit := range rankByRelease(hits, videoName) {
			if i == 3 {
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			data, name, err := hit.download(ctx)
			cancel()
			if err == nil && len(data) > 0 {
				return data, name, nil
			}
			if err != nil {
				lastErr = fmt.Errorf("%s: %w", s.name, err)
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no %s subtitles", q.lang)
	}
	return nil, "", lastErr
}

// rankByRelease orders hits by how many release-name tokens they share with
// the downloaded file (WEB-DL vs BluRay, the release group), so the subtitle
// is most likely timed for this exact cut. Ties keep the source's own order,
// which is already by popularity.
func rankByRelease(hits []subtitleHit, videoName string) []subtitleHit {
	want := releaseTokens(strings.TrimSuffix(videoName, filepath.Ext(videoName)))
	type scored struct {
		hit   subtitleHit
		score int
	}
	list := make([]scored, len(hits))
	for i, h := range hits {
		score := 0
		for tok := range releaseTokens(h.release) {
			if want[tok] {
				score++
			}
		}
		list[i] = scored{h, score}
	}
	// Stable insertion sort: lists are short and ties must keep their order.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].score > list[j-1].score; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	out := make([]subtitleHit, len(list))
	for i, s := range list {
		out[i] = s.hit
	}
	return out
}

func releaseTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(f) > 1 {
			out[f] = true
		}
	}
	return out
}

// writeLanguageSidecar saves a subtitle as Movie.<lang>.<ext> beside the
// video — the naming players use to auto-load and label an external track.
func writeLanguageSidecar(videoPath, lang, subName string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(subName))
	switch ext {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub":
	default:
		ext = ".srt"
	}
	path := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + "." + lang + ext
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("save subtitle: %w", err)
	}
	return path, nil
}

// Package stream downloads a remote file over a single connection while serving
// it to a local video player at the same time. The player streams from
// localhost as the file fills, and when the download finishes the file is a
// complete, saved copy — all from one upstream connection.
package stream

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Server tees one upstream download to a save file and a localhost HTTP server.
type Server struct {
	remoteURL string
	savePath  string
	client    *http.Client

	mu         sync.Mutex
	cond       *sync.Cond
	downloaded int64
	done       bool
	failed     error
	paused     bool
	stopped    bool

	total   atomic.Int64 // content length, -1 until known / if unknown
	cancel  context.CancelFunc
	httpSrv *http.Server
	local   string
}

// New prepares a server that will download remoteURL to savePath.
func New(remoteURL, savePath string) *Server {
	s := &Server{
		remoteURL: remoteURL,
		savePath:  savePath,
		// Untimed: a movie transfer far outruns any API timeout; cancellation
		// is via context instead.
		client: &http.Client{},
	}
	s.cond = sync.NewCond(&s.mu)
	s.total.Store(-1)
	return s
}

// Start opens the single upstream connection, begins downloading to the save
// file, and starts the localhost server. It returns the URL to hand the player.
func (s *Server) Start(ctx context.Context) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.remoteURL, nil)
	if err != nil {
		cancel()
		return "", err
	}
	req.Header.Set("User-Agent", "MovieFinder/0.1")

	resp, err := s.client.Do(req)
	if err != nil {
		cancel()
		return "", fmt.Errorf("open stream: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		cancel()
		return "", fmt.Errorf("stream source answered %s", resp.Status)
	}
	s.total.Store(resp.ContentLength) // -1 when the host omits Content-Length

	out, err := os.Create(s.savePath)
	if err != nil {
		resp.Body.Close()
		cancel()
		return "", fmt.Errorf("create save file: %w", err)
	}

	go s.download(resp.Body, out)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return "", fmt.Errorf("start local server: %w", err)
	}
	// Give the local URL the real file extension so the player recognises the
	// container.
	ext := path.Ext(strings.SplitN(path.Base(s.savePath), "?", 2)[0])
	s.local = fmt.Sprintf("http://%s/video%s", ln.Addr().String(), ext)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serve)
	s.httpSrv = &http.Server{Handler: mux}
	go s.httpSrv.Serve(ln)

	return s.local, nil
}

// download copies the single upstream connection into the save file, publishing
// progress so the serving side can hand out bytes as they arrive.
func (s *Server) download(body io.ReadCloser, out *os.File) {
	defer body.Close()

	buf := make([]byte, 256*1024)
	for {
		// Honour a pause before reading more. Not reading from the connection
		// applies TCP backpressure, so the sender pauses too and the single
		// connection stays open.
		if !s.waitWhilePaused() {
			return // stopped while paused
		}
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				s.finish(werr)
				return
			}
			s.mu.Lock()
			s.downloaded += int64(n)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		if err == io.EOF {
			out.Close()
			s.finish(nil)
			return
		}
		if err != nil {
			out.Close()
			s.finish(err)
			return
		}
	}
}

func (s *Server) finish(err error) {
	s.mu.Lock()
	s.done = true
	s.failed = err
	s.cond.Broadcast()
	s.mu.Unlock()
}

// waitWhilePaused blocks while paused. It returns false if the server was
// stopped while paused, so the caller can bail out.
func (s *Server) waitWhilePaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.paused && !s.stopped {
		s.cond.Wait()
	}
	return !s.stopped
}

// Pause suspends the download (and, once the player's buffer drains, playback).
func (s *Server) Pause() {
	s.mu.Lock()
	s.paused = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Resume continues a paused download.
func (s *Server) Resume() {
	s.mu.Lock()
	s.paused = false
	s.cond.Broadcast()
	s.mu.Unlock()
}

// IsPaused reports whether the download is currently paused.
func (s *Server) IsPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// serve streams the (possibly still-growing) save file to the player, honouring
// Range requests by waiting for bytes that have not been downloaded yet.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	total := s.total.Load()
	start, end, isRange := parseRange(r.Header.Get("Range"), total)

	f, err := os.Open(s.savePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentType(s.savePath))
	switch {
	case total >= 0 && isRange:
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
	case total >= 0:
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}

	pos := start
	buf := make([]byte, 256*1024)
	for {
		if total >= 0 && pos > end {
			return
		}
		avail, done, ferr := s.waitFor(pos)
		if ferr != nil {
			return
		}
		if avail <= pos {
			if done {
				return // no more data will arrive
			}
			continue
		}

		toRead := int64(len(buf))
		if avail-pos < toRead {
			toRead = avail - pos
		}
		if total >= 0 && pos+toRead > end+1 {
			toRead = end + 1 - pos
		}

		n, rerr := f.ReadAt(buf[:toRead], pos)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // player disconnected; the download keeps running
			}
			pos += int64(n)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		if rerr != nil && rerr != io.EOF {
			return
		}
	}
}

// waitFor blocks until at least pos+1 bytes are available or the download ends
// (finished or stopped).
func (s *Server) waitFor(pos int64) (avail int64, done bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.downloaded <= pos && !s.done && !s.stopped {
		s.cond.Wait()
	}
	return s.downloaded, s.done || s.stopped, s.failed
}

// Progress reports how far the download has got. total is -1 when unknown.
func (s *Server) Progress() (downloaded, total int64, done bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.downloaded, s.total.Load(), s.done, s.failed
}

// LocalURL is the address handed to the player.
func (s *Server) LocalURL() string { return s.local }

// Stop shuts down the local server and cancels the download. Call it to cancel,
// when the stream is replaced, or when the app closes.
func (s *Server) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.paused = false // so a paused download loop can wake and exit
	s.cond.Broadcast()
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
}

// Stopped reports whether the download was cancelled.
func (s *Server) Stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// parseRange handles a single "bytes=start-end" range. Multi-range requests
// (rare from players) collapse to their first range.
func parseRange(header string, total int64) (start, end int64, ok bool) {
	if !strings.HasPrefix(header, "bytes=") {
		if total >= 0 {
			return 0, total - 1, false
		}
		return 0, -1, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i]
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		if total >= 0 {
			return 0, total - 1, false
		}
		return 0, -1, false
	}

	startStr, endStr := spec[:dash], spec[dash+1:]
	if startStr == "" {
		// suffix range: last N bytes
		if n, err := strconv.ParseInt(endStr, 10, 64); err == nil && total >= 0 {
			start = total - n
			if start < 0 {
				start = 0
			}
			return start, total - 1, true
		}
		return 0, total - 1, false
	}
	start, _ = strconv.ParseInt(startStr, 10, 64)
	if endStr == "" {
		if total >= 0 {
			end = total - 1
		} else {
			end = -1
		}
	} else {
		end, _ = strconv.ParseInt(endStr, 10, 64)
	}
	return start, end, true
}

func contentType(savePath string) string {
	switch strings.ToLower(path.Ext(savePath)) {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

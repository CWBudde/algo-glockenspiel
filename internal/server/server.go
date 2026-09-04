// Package server hosts the glockenspiel web app over HTTP.
//
// The whole app is served from disk, out of the --dist directory that
// `just build-web` fills: the React bundle, its content-hashed assets and the
// WebAssembly module all land there. The binary embeds one page, a placeholder
// naming the build command, and answers with it when the build is missing; see
// web/embed.go for why nothing else is embedded and MissingAppError below for
// what a user sees when the build step was skipped.
//
// Fitting over HTTP is Phase 4.2 and lives beside this file: job.go owns the
// job history and the queue that feeds the one fit slot, fit.go the JSON
// endpoints for the most recent job, jobs.go the per-job endpoints, restore.go
// the startup scan that rebuilds the history from the work directory,
// events.go the SSE progress stream and params.go the request parsing. This
// file stays the static, read-only half.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// wasmFileName is the module scripts/build-wasm.sh writes into web/dist.
	wasmFileName = "glockenspiel.wasm"

	// indexFileName is the built page served for the site root. The front end
	// asks for the module and the manifest by paths relative to it, which is
	// what lets one bundle work at the server root and under the GitHub Pages
	// project sub-path.
	indexFileName = "index.html"

	// assetsDirName is the sub-directory Vite writes the content-hashed
	// bundle into. Its file names carry the hash, which is what lets the
	// files under it be cached indefinitely; see cacheControlFor.
	assetsDirName = "assets"

	// placeholderFileName is the embedded page shown when the build is
	// missing. It is the only file compiled into the binary.
	placeholderFileName = "placeholder.html"

	// defaultShutdownTimeout bounds how long a graceful shutdown waits for
	// in-flight requests before the process gives up on them.
	defaultShutdownTimeout = 5 * time.Second

	// readHeaderTimeout keeps a stalled client from pinning a connection
	// forever. The server is a local development tool, so the value only has
	// to be sane, not tuned.
	readHeaderTimeout = 10 * time.Second
)

// Config describes one server instance. The zero value is not usable: New
// checks what it can check without opening a port -- that a static tree is
// configured and that it carries an index page -- while Addr is only validated
// when Run hands it to net.Listen.
type Config struct {
	// Addr is the listen address in net.Listen form, such as ":8080". An
	// empty port (":0") is useful in tests: Run reports the chosen address.
	Addr string

	// Version is reported by the version endpoint.
	Version string

	// Static is the embedded fallback tree, rooted so that placeholder.html
	// is at the top. Pass web.StaticFS().
	Static fs.FS

	// DistDir is the directory holding the built app and the generated
	// WebAssembly module, normally web/dist. It may be missing on disk;
	// requests then fail loudly rather than silently.
	DistDir string

	// Log receives the startup and shutdown lines. A nil Log discards them.
	Log io.Writer

	// ShutdownTimeout bounds the graceful shutdown. Zero means
	// defaultShutdownTimeout.
	ShutdownTimeout time.Duration

	// MaxReferenceBytes caps an uploaded reference recording. Zero means
	// fitschema.DefaultMaxReferenceBytes; see there for why that number.
	MaxReferenceBytes int64

	// WorkDir is where every served fit writes its run directory, one
	// directory per job, in the layout internal/fitrun defines and the
	// campaign's collect step already reads. An empty value means
	// DefaultWorkDir.
	WorkDir string
}

// DefaultWorkDir is where served fits land when nothing names a directory: a
// "glockenspiel/fits" tree under the user's cache directory.
//
// The cache directory rather than the working directory, because `serve` is
// run from wherever the user happens to be and a run directory that appeared
// under whatever that was would be a surprise. It falls back to "out/serve"
// only when the platform has no cache directory to offer.
func DefaultWorkDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return filepath.FromSlash("out/serve")
	}

	return filepath.Join(cache, "glockenspiel", "fits")
}

// staticAsset is one embedded file, prepared once at startup. The tree is a
// handful of small text files, so holding it in memory costs nothing and buys
// a stable ETag per file.
type staticAsset struct {
	data        []byte
	contentType string
	etag        string
}

// Server serves the web app. Create it with New and run it with Run.
type Server struct {
	config Config
	assets map[string]staticAsset

	// jobs owns the history and the queue that feeds the one fit slot. It is
	// reached from every request goroutine and from the goroutine running a
	// fit, and is safe for all of them.
	jobs jobManager

	// shutdown is closed once, before http.Server.Shutdown is called, to end
	// the responses that would otherwise never end. See Stop.
	shutdown     chan struct{}
	shutdownOnce sync.Once

	// compareSlots bounds how many /compare requests may build their payload
	// at once. That endpoint allocates two signals and two full-resolution
	// spectrogram views before anything is reduced -- at 192 kHz, on the order
	// of 270 MB per request -- and it is a free, repeatable GET, so nothing
	// else stops a handful of concurrent requests (or reloads of the same tab)
	// from adding those up. A full channel is a 503 rather than a slot to wait
	// for: a client that is refused can retry, while one left blocked behind
	// requests it cannot see would just be a slower way to exhaust the same
	// memory.
	compareSlots chan struct{}
}

// maxConcurrentCompares is compareSlots' capacity. It is not configurable:
// unlike MaxReferenceBytes, which bounds a single request's own cost, this
// bounds how many of them may run together, and a handful is enough headroom
// for the compare view and an audition tab open side by side without leaving
// the endpoint effectively unbounded again.
const maxConcurrentCompares = 4

// New prepares a server from cfg. It fails when the embedded tree is unusable,
// which would be a build problem rather than a user error, and is therefore
// worth catching before a port is opened.
func New(cfg Config) (*Server, error) {
	if cfg.Static == nil {
		return nil, errors.New("server: no static file system configured")
	}

	assets, err := loadStaticAssets(cfg.Static)
	if err != nil {
		return nil, err
	}

	if _, ok := assets[placeholderFileName]; !ok {
		return nil, fmt.Errorf("server: embedded web tree has no %s", placeholderFileName)
	}

	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}

	if cfg.WorkDir == "" {
		cfg.WorkDir = DefaultWorkDir()
	}

	srv := &Server{
		config:       cfg,
		assets:       assets,
		shutdown:     make(chan struct{}),
		compareSlots: make(chan struct{}, maxConcurrentCompares),
	}

	// The work directory is read back before the first request, so a restart
	// does not lose the history: a run directory is the whole record of a fit,
	// and every one of them still on disk becomes a job the read endpoints can
	// answer for. A work directory that cannot be read is logged and survived
	// -- it costs the history, not the server.
	srv.restoreJobs()

	return srv, nil
}

// loadStaticAssets reads the whole embedded tree into memory, computing the
// content type and a content hash per file. Only regular files are recorded,
// which is what keeps directory listings from existing at all: a request that
// resolves to a directory simply finds no asset.
func loadStaticAssets(files fs.FS) (map[string]staticAsset, error) {
	assets := make(map[string]staticAsset)

	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		data, readErr := fs.ReadFile(files, name)
		if readErr != nil {
			return fmt.Errorf("server: read embedded %s: %w", name, readErr)
		}

		sum := sha256.Sum256(data)
		assets[name] = staticAsset{
			data:        data,
			contentType: contentTypeFor(name),
			etag:        formatETag(sum[:]),
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return assets, nil
}

// etagForReader hashes the whole file and rewinds it, so that the validator
// describes the bytes that are about to be served. One extra read pass over a
// few megabytes is cheap next to serving them, and it is the only way to tell a
// same-second rebuild apart from the version already in the browser cache.
func etagForReader(file *os.File) (string, error) {
	digest := sha256.New()

	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash artifact: %w", err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind artifact: %w", err)
	}

	return formatETag(digest.Sum(nil)), nil
}

// formatETag renders half a SHA-256 digest as a strong HTTP entity tag. Half is
// plenty: the tag only has to distinguish builds, not resist forgery.
func formatETag(sum []byte) string {
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", s.handleVersion)

	// The fit family. These are exact patterns, so an unknown path under
	// /api/fit/ would otherwise fall through to the static handler and be
	// answered as a missing asset; the subtree pattern below keeps it inside
	// the API. It does not shadow the exact patterns, which ServeMux prefers
	// as the more specific match, and it does not cause a redirect from
	// "/api/fit" either, because that path is registered in its own right.
	mux.HandleFunc("/api/fit", s.handleFitStatus)
	mux.HandleFunc("/api/fit/start", s.handleFitStart)
	mux.HandleFunc("/api/fit/cancel", s.handleFitCancel)
	mux.HandleFunc("/api/fit/events", s.handleFitEvents)
	mux.HandleFunc("/api/fit/preset", s.handleFitPreset)
	mux.HandleFunc("/api/fit/audio", s.handleFitAudio)

	// The per-job family. The wildcard patterns are more specific than the
	// subtree pattern below, which ServeMux prefers, so they are matched
	// first; a path under /api/fit/jobs/ that none of them describes still
	// lands on the 404 rather than on the static tree.
	mux.HandleFunc("/api/fit/jobs", s.handleFitJobs)
	mux.HandleFunc("/api/fit/jobs/{id}", s.handleFitJob)
	mux.HandleFunc("/api/fit/jobs/{id}/preset", s.handleFitJobPreset)
	mux.HandleFunc("/api/fit/jobs/{id}/audio", s.handleFitJobAudio)
	mux.HandleFunc("/api/fit/jobs/{id}/trace", s.handleFitJobTrace)
	mux.HandleFunc("/api/fit/jobs/{id}/reference", s.handleFitJobReference)
	mux.HandleFunc("/api/fit/jobs/{id}/compare", s.handleFitJobCompare)

	mux.HandleFunc("/api/fit/", http.NotFound)

	mux.HandleFunc("/", s.handleStatic)

	return mux
}

// handleVersion reports the build version the same string `glockenspiel
// version` prints, so a browser can tell which binary it is talking to.
func (s *Server) handleVersion(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The answer describes the running process, so a cached copy could
	// outlive the binary it describes.
	writer.Header().Set("Cache-Control", "no-store")

	body := struct {
		Version string `json:"version"`
	}{Version: s.config.Version}

	if err := json.NewEncoder(writer).Encode(body); err != nil {
		s.logf("version response failed: %v", err)
	}
}

// handleStatic serves the built app from the dist directory on disk.
//
// Anything that is not a file in that tree is a 404: there is no directory
// listing and no fallback to index.html, because a silent fallback would turn a
// mistyped asset path into a page that loads and then misbehaves. The front end
// routes on the URL fragment for exactly that reason -- a fragment never
// reaches this handler, so a second tab costs no route here.
func (s *Server) handleStatic(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	name := strings.TrimPrefix(request.URL.Path, "/")
	if name == "" {
		name = indexFileName
	}

	// net/http normalises "." and ".." out of the request path before a
	// handler runs, but it only knows forward slashes. A percent-encoded
	// backslash survives that pass, and on Windows filepath.Join would read it
	// as a separator again, so containment is checked here with OS-native
	// semantics rather than trusted from the URL: fs.ValidPath rejects
	// anything that is not a clean, relative, slash-separated path,
	// filepath.IsLocal rejects what the local OS would still read as an escape
	// -- backslashes, drive letters, reserved device names -- and os.OpenInRoot
	// refuses to leave the directory even through a symlink.
	if !fs.ValidPath(name) || name == "." {
		http.NotFound(writer, request)

		return
	}

	localName := filepath.FromSlash(name)
	if !filepath.IsLocal(localName) {
		http.NotFound(writer, request)

		return
	}

	if s.config.DistDir == "" {
		s.writeDistError(writer, request, name, fmt.Errorf("no dist directory configured: %w", fs.ErrNotExist))

		return
	}

	file, err := os.OpenInRoot(s.config.DistDir, localName)
	if err != nil {
		s.writeDistError(writer, request, name, err)

		return
	}

	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(writer, request)

		return
	}

	// The validator is derived from the bytes, not from the modification
	// time. scripts/build-wasm.sh rewrites the module in place under the same
	// name, and HTTP dates have whole-second granularity, so a rebuild
	// finished within the same second as the previous one would be answered
	// with a 304 and the browser would keep running the old module.
	etag, err := etagForReader(file)
	if err != nil {
		s.writeDistError(writer, request, name, err)

		return
	}

	writer.Header().Set("Content-Type", contentTypeFor(name))
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", cacheControlFor(name))
	// A zero modtime suppresses Last-Modified, and with it the coarse
	// If-Modified-Since path; the ETag above is the only validator.
	http.ServeContent(writer, request, name, time.Time{}, file)
}

// cacheControlFor picks the caching policy for a file in the dist tree.
//
// Vite writes the bundle under assets/ with a content hash in every file name,
// so those URLs can never change meaning: a rebuild produces new names rather
// than new bytes behind an old name. They are worth caching for good, which is
// what "immutable" buys -- the browser stops revalidating them even on a
// reload.
//
// Everything else keeps a fixed name -- index.html, glockenspiel.wasm,
// manifest.json, wasm_exec.js -- so the browser has to ask whether its copy is
// still current. The ETag keeps that question down to a 304.
func cacheControlFor(name string) string {
	if strings.HasPrefix(name, assetsDirName+"/") {
		return "public, max-age=31536000, immutable"
	}

	return "no-cache"
}

// writeDistError answers a request for a file that could not be opened.
//
// Two missing files are the interesting case: index.html and the WebAssembly
// module. Either means a build step was skipped, so they earn a 503 carrying
// the fix rather than a bare 404 that the browser console would show as an
// anonymous failed fetch. Everything else -- a permission problem, an I/O
// error, a symlink leaving the directory -- is not fixed by rebuilding, so it
// must not send the user to `just build-web`.
func (s *Server) writeDistError(writer http.ResponseWriter, request *http.Request, name string, cause error) {
	if !errors.Is(cause, fs.ErrNotExist) {
		s.logf("serving %s failed: %v", name, cause)
		http.Error(writer, "the build artifact could not be read; see the server log", http.StatusInternalServerError)

		return
	}

	switch name {
	case indexFileName:
		s.logf("request for %s failed: %v", name, cause)
		s.writePlaceholder(writer, request)

	case wasmFileName:
		s.logf("request for %s failed: %v", name, cause)

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusServiceUnavailable)

		_, _ = io.WriteString(writer, MissingWasmMessage(s.config.DistDir))

	default:
		http.NotFound(writer, request)
	}
}

// writePlaceholder answers a request for the site root when the app has not
// been built. It is a page rather than a line of text because it is what a
// browser lands on, and it is a 503 for the same reason the module's message
// is -- the file is expected to exist and the fix is a command, not a
// different URL.
func (s *Server) writePlaceholder(writer http.ResponseWriter, request *http.Request) {
	asset, ok := s.assets[placeholderFileName]
	if !ok {
		// New refuses to build a server without it, so this is unreachable
		// short of the map being mutated; answer honestly rather than panic.
		http.Error(writer, MissingAppMessage(s.config.DistDir), http.StatusServiceUnavailable)

		return
	}

	writer.Header().Set("Content-Type", asset.contentType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)

	if request.Method == http.MethodHead {
		return
	}

	_, _ = writer.Write(asset.data)
}

// MissingAppError reports why the built app cannot be served, or nil when it
// is in place. Callers use it to warn at startup; the handlers repeat the
// check per request so that a build finished after startup is picked up
// without a restart.
func (s *Server) MissingAppError() error {
	return s.missingFileError(indexFileName)
}

// MissingWasmError reports the same for the WebAssembly module. The two are
// separate because they come from separate build steps and either can be
// missing on its own.
func (s *Server) MissingWasmError() error {
	return s.missingFileError(wasmFileName)
}

func (s *Server) missingFileError(name string) error {
	if s.config.DistDir == "" {
		return errors.New("no dist directory configured")
	}

	info, err := os.Stat(filepath.Join(s.config.DistDir, name))
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("%s is a directory", name)
	}

	return nil
}

// MissingAppMessage explains a missing app bundle and names the fix. The same
// text goes to the terminal at startup and, through the placeholder page, to
// the browser, so both places say exactly one thing.
func MissingAppMessage(distDir string) string {
	return missingBuildMessage(distDir, indexFileName, "The web app has not been built")
}

// MissingWasmMessage explains a missing WebAssembly module and names the fix.
func MissingWasmMessage(distDir string) string {
	return missingBuildMessage(distDir, wasmFileName, "The WebAssembly module is missing")
}

func missingBuildMessage(distDir, name, headline string) string {
	if distDir == "" {
		distDir = filepath.FromSlash("web/dist")
	}

	return fmt.Sprintf(
		"%s: %s was not found.\n"+
			"It is a build artifact and is not part of a checkout. Build it with "+
			"`just build-web` and reload this page.\n",
		headline,
		filepath.Join(distDir, name),
	)
}

// Run serves until ctx is cancelled, then shuts down gracefully. The caller
// owns the signal handling -- internal/cli/serve.go derives ctx from
// signal.NotifyContext exactly as fit does -- so this package stays free of
// process-wide concerns.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.config.Addr, err)
	}

	httpServer := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	s.logf("serving the glockenspiel web app on http://%s", listener.Addr())

	served := make(chan error, 1)

	go func() {
		served <- httpServer.Serve(listener)
	}()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
		s.logf("shutting down")

		// Before Shutdown, not after. Shutdown waits for active connections to
		// finish, and an SSE progress stream is an active connection forever:
		// leaving one open would burn the entire ShutdownTimeout on every
		// Ctrl-C and turn a graceful exit into a five-second hang. This also
		// ends the fit itself, so a search does not keep a core busy after the
		// server that owns it has gone.
		s.Stop()

		// The shutdown deadline must not hang off the cancelled ctx, or
		// in-flight requests would be cut off immediately instead of being
		// given their grace period.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.config.ShutdownTimeout)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}

		return nil
	}
}

// Stop releases everything that would otherwise outlive a graceful shutdown:
// the never-ending SSE responses, and the fit they are reporting on.
//
// Run calls it, so a caller that uses Run needs nothing else. It is exported
// for callers that mount Handler in a server of their own -- and for tests --
// because without it a fit goroutine survives the thing that started it.
//
// It is idempotent: closing a closed channel panics, and cancelling a fit that
// has already finished is a no-op by construction.
func (s *Server) Stop() {
	s.shutdownOnce.Do(func() {
		close(s.shutdown)
	})

	s.jobs.stopAll()
}

// logf writes one line to the configured log sink, if there is one.
func (s *Server) logf(format string, args ...any) {
	if s.config.Log == nil {
		return
	}

	_, _ = fmt.Fprintf(s.config.Log, format+"\n", args...)
}

// allowReadMethods rejects everything but GET and HEAD. The server has no
// state to change yet, so anything else is a client mistake worth naming.
func allowReadMethods(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}

	writer.Header().Set("Allow", "GET, HEAD")
	http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)

	return false
}

// contentTypeFor maps a file name to its media type. The table is explicit
// rather than mime.TypeByExtension because the system mime database decides
// what a .js or .wasm file is on some hosts and not on others, and a wasm
// module served as octet-stream loses instantiateStreaming.
func contentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	case ".md", ".txt":
		return "text/plain; charset=utf-8"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

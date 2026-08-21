// Package server hosts the glockenspiel web app over HTTP.
//
// The hand-written part of the app is served from an embedded file system, the
// generated WebAssembly module from disk. That split exists because web/dist is
// gitignored and only appears after `just build-web`; see web/embed.go for the
// reasoning and MissingWasmError below for what a user sees when the build step
// was skipped.
//
// Fitting over HTTP -- the job manager, the JSON endpoints and the SSE progress
// stream -- is Phase 4.2 and deliberately absent here.
package server

import (
	"bytes"
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
	"time"
)

const (
	// distURLPrefix is where the generated artifacts live in the URL space.
	// web/main.js fetches "dist/glockenspiel.wasm" relative to the page, so
	// this path is fixed by the front end, not a free choice.
	distURLPrefix = "/dist/"

	// wasmFileName is the module scripts/build-wasm.sh writes into web/dist.
	wasmFileName = "glockenspiel.wasm"

	// indexFileName is served for the site root.
	indexFileName = "index.html"

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

	// Static is the embedded web tree, rooted so that index.html is at the
	// top. Pass web.StaticFS().
	Static fs.FS

	// DistDir is the directory holding the generated WebAssembly module,
	// normally web/dist. It may be missing on disk; requests for the module
	// then fail loudly rather than silently.
	DistDir string

	// Log receives the startup and shutdown lines. A nil Log discards them.
	Log io.Writer

	// ShutdownTimeout bounds the graceful shutdown. Zero means
	// defaultShutdownTimeout.
	ShutdownTimeout time.Duration
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
}

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

	if _, ok := assets[indexFileName]; !ok {
		return nil, fmt.Errorf("server: embedded web tree has no %s", indexFileName)
	}

	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}

	return &Server{config: cfg, assets: assets}, nil
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

// MissingWasmError reports why the WebAssembly module cannot be served, or nil
// when it is in place. Callers use it to warn at startup; the handlers repeat
// the check per request so that a build finished after startup is picked up
// without a restart.
func (s *Server) MissingWasmError() error {
	if s.config.DistDir == "" {
		return errors.New("no dist directory configured")
	}

	info, err := os.Stat(filepath.Join(s.config.DistDir, wasmFileName))
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("%s is a directory", wasmFileName)
	}

	return nil
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc(distURLPrefix, s.handleDist)
	// A subtree pattern makes ServeMux redirect "/dist" to "/dist/". Register
	// the bare path as well so that a request for the directory is the plain
	// 404 the documentation promises, rather than a 301 into a 404.
	mux.HandleFunc(strings.TrimSuffix(distURLPrefix, "/"), http.NotFound)
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

// handleStatic serves the embedded tree. Anything that is not a known file is
// a 404: there is no directory listing and no fallback to index.html, because a
// silent fallback would turn a mistyped asset path into a page that loads and
// then misbehaves.
func (s *Server) handleStatic(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	name := strings.TrimPrefix(request.URL.Path, "/")
	if name == "" {
		name = indexFileName
	}

	asset, ok := s.assets[name]
	if !ok {
		http.NotFound(writer, request)

		return
	}

	writer.Header().Set("Content-Type", asset.contentType)
	writer.Header().Set("ETag", asset.etag)
	// Nothing here is content-addressed -- fingerprinted asset names are
	// Phase 5.3 -- so the browser must revalidate rather than sit on a stale
	// copy. The ETag keeps that revalidation down to a 304.
	writer.Header().Set("Cache-Control", "no-cache")

	// A zero modtime suppresses Last-Modified, which would otherwise be the
	// build time of the binary and carries no information the ETag lacks.
	http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(asset.data))
}

// handleDist serves the generated artifacts from disk.
func (s *Server) handleDist(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	name := strings.TrimPrefix(request.URL.Path, distURLPrefix)

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
	writer.Header().Set("Cache-Control", "no-cache")
	// A zero modtime suppresses Last-Modified, and with it the coarse
	// If-Modified-Since path; the ETag above is the only validator.
	http.ServeContent(writer, request, name, time.Time{}, file)
}

// writeDistError answers a request for an artifact that could not be opened.
//
// A missing module is the interesting case: it means a build step was skipped,
// so it earns a 503 carrying the fix rather than a bare 404 that the browser
// console would show as an anonymous failed fetch. Everything else -- a
// permission problem, an I/O error, a symlink leaving the directory -- is not
// fixed by rebuilding, so it must not send the user to `just build-web`.
func (s *Server) writeDistError(writer http.ResponseWriter, request *http.Request, name string, cause error) {
	if !errors.Is(cause, fs.ErrNotExist) {
		s.logf("serving %s failed: %v", name, cause)
		http.Error(writer, "the build artifact could not be read; see the server log", http.StatusInternalServerError)

		return
	}

	if name != wasmFileName {
		http.NotFound(writer, request)

		return
	}

	s.logf("request for %s failed: %v", name, cause)

	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)

	_, _ = io.WriteString(writer, MissingWasmMessage(s.config.DistDir))
}

// MissingWasmMessage explains a missing WebAssembly module and names the fix.
// The same text goes to the terminal at startup and to the browser on request,
// so both places say exactly one thing.
func MissingWasmMessage(distDir string) string {
	if distDir == "" {
		distDir = filepath.FromSlash("web/dist")
	}

	return fmt.Sprintf(
		"The WebAssembly module is missing: %s was not found.\n"+
			"It is a build artifact and is not part of a checkout. Build it with `just build-web` "+
			"(or ./scripts/build-wasm.sh) and reload this page.\n",
		filepath.Join(distDir, wasmFileName),
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

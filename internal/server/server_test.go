package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cwbudde/glockenspiel/internal/server"
	"github.com/cwbudde/glockenspiel/web"
)

// testTree stands in for the embedded web tree: one page, one script, one
// nested asset, which is enough to exercise routing, ETags and listings.
func testTree() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<!doctype html><title>glockenspiel</title>")},
		"main.js":                &fstest.MapFile{Data: []byte("// main")},
		"assets/glockenmark.svg": &fstest.MapFile{Data: []byte("<svg/>")},
	}
}

func newTestServer(t *testing.T, distDir string) *server.Server {
	t.Helper()

	srv, err := server.New(server.Config{
		Addr:    "127.0.0.1:0",
		Version: "test-version",
		Static:  testTree(),
		DistDir: distDir,
		Log:     io.Discard,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	return srv
}

func get(t *testing.T, handler http.Handler, target string) *http.Response {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

	return recorder.Result()
}

func TestNewRejectsUnusableConfig(t *testing.T) {
	if _, err := server.New(server.Config{}); err == nil {
		t.Fatal("expected a server without a static file system to fail")
	}

	empty := fstest.MapFS{"main.js": &fstest.MapFile{Data: []byte("// main")}}
	if _, err := server.New(server.Config{Static: empty}); err == nil {
		t.Fatal("expected a tree without index.html to fail")
	}
}

func TestStaticRoutes(t *testing.T) {
	handler := newTestServer(t, t.TempDir()).Handler()

	tests := []struct {
		name        string
		target      string
		wantStatus  int
		wantType    string
		wantContent string
	}{
		{name: "root serves index", target: "/", wantStatus: http.StatusOK, wantType: "text/html; charset=utf-8", wantContent: "glockenspiel"},
		{name: "index by name", target: "/index.html", wantStatus: http.StatusOK, wantType: "text/html; charset=utf-8", wantContent: "glockenspiel"},
		{name: "script", target: "/main.js", wantStatus: http.StatusOK, wantType: "text/javascript; charset=utf-8", wantContent: "// main"},
		{name: "nested asset", target: "/assets/glockenmark.svg", wantStatus: http.StatusOK, wantType: "image/svg+xml", wantContent: "<svg/>"},
		{name: "unknown file", target: "/nope.js", wantStatus: http.StatusNotFound},
		{name: "embed.go is not part of the tree", target: "/embed.go", wantStatus: http.StatusNotFound},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := get(t, handler, testCase.target)
			defer func() {
				_ = response.Body.Close()
			}()

			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.wantStatus)
			}

			if testCase.wantStatus != http.StatusOK {
				return
			}

			if got := response.Header.Get("Content-Type"); got != testCase.wantType {
				t.Fatalf("content type = %q, want %q", got, testCase.wantType)
			}

			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if !strings.Contains(string(body), testCase.wantContent) {
				t.Fatalf("body = %q, want it to contain %q", body, testCase.wantContent)
			}
		})
	}
}

// A directory listing would leak the shape of the tree and, worse, suggest the
// server is a file browser. Neither a directory nor its trailing-slash form may
// produce one.
func TestNoDirectoryListing(t *testing.T) {
	handler := newTestServer(t, t.TempDir()).Handler()

	for _, target := range []string{"/assets", "/assets/", "/dist/", "/dist"} {
		response := get(t, handler, target)

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		_ = response.Body.Close()

		if response.StatusCode == http.StatusOK {
			t.Fatalf("%s returned 200, expected no listing", target)
		}

		if strings.Contains(string(body), "glockenmark.svg") {
			t.Fatalf("%s leaked a directory listing: %q", target, body)
		}
	}
}

func TestVersionEndpoint(t *testing.T) {
	response := get(t, newTestServer(t, t.TempDir()).Handler(), "/api/version")
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}

	var body struct {
		Version string `json:"version"`
	}

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Version != "test-version" {
		t.Fatalf("version = %q, want %q", body.Version, "test-version")
	}
}

func TestStaticRevalidatesWithETag(t *testing.T) {
	handler := newTestServer(t, t.TempDir()).Handler()

	first := get(t, handler, "/index.html")
	_ = first.Body.Close()

	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag on an embedded asset")
	}

	if got := first.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache control = %q, want no-cache", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	request.Header.Set("If-None-Match", etag)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", recorder.Code)
	}
}

func TestWasmServedFromDisk(t *testing.T) {
	distDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(distDir, "glockenspiel.wasm"), []byte("\x00asm"), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	srv := newTestServer(t, distDir)
	if err := srv.MissingWasmError(); err != nil {
		t.Fatalf("expected the module to be found: %v", err)
	}

	response := get(t, srv.Handler(), "/dist/glockenspiel.wasm")
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); got != "application/wasm" {
		t.Fatalf("content type = %q, want application/wasm", got)
	}
}

// The whole point of not embedding web/dist: a missing module must say what to
// do about it instead of failing as an anonymous 404.
func TestMissingWasmExplainsTheFix(t *testing.T) {
	distDir := t.TempDir()

	srv := newTestServer(t, distDir)
	if srv.MissingWasmError() == nil {
		t.Fatal("expected a missing module to be reported")
	}

	response := get(t, srv.Handler(), "/dist/glockenspiel.wasm")

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}

	if !strings.Contains(string(body), "just build-web") {
		t.Fatalf("body = %q, want it to name the fix", body)
	}

	// The page itself still loads, so the browser reaches the JavaScript that
	// surfaces the message rather than showing nothing at all.
	index := get(t, srv.Handler(), "/")
	_ = index.Body.Close()

	if index.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d, want 200", index.StatusCode)
	}

	// Some other missing artifact is an ordinary 404: only the module gets
	// the build-step explanation.
	other := get(t, srv.Handler(), "/dist/other.bin")
	_ = other.Body.Close()

	if other.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", other.StatusCode)
	}
}

// Paths that try to climb out of the dist directory must never yield the file
// they point at. net/http normalises "../" segments before the handler runs, so
// this pins the outcome rather than the mechanism: whatever the status, the
// content outside dist stays unreachable.
func TestDistRefusesEscapingPaths(t *testing.T) {
	distDir := t.TempDir()
	outsideDir := filepath.Dir(distDir)

	const secret = "TOP-LEVEL-SECRET"

	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte(secret), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	handler := newTestServer(t, distDir).Handler()

	for _, target := range []string{"/dist/../outside.txt", "/dist/%2e%2e/outside.txt", "/dist/sub/../../outside.txt"} {
		response := get(t, handler, target)

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		_ = response.Body.Close()

		if response.StatusCode == http.StatusOK {
			t.Fatalf("%s returned 200", target)
		}

		if strings.Contains(string(body), secret) {
			t.Fatalf("%s escaped the dist directory: %q", target, body)
		}
	}
}

func TestWriteMethodsRejected(t *testing.T) {
	handler := newTestServer(t, t.TempDir()).Handler()

	for _, target := range []string{"/", "/api/version", "/dist/glockenspiel.wasm"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s = %d, want 405", target, recorder.Code)
		}

		if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
			t.Fatalf("allow header = %q", got)
		}
	}
}

// Run must serve until its context ends and then come back on its own, which is
// what makes Ctrl-C in the terminal a clean shutdown rather than a killed
// process.
func TestRunServesAndShutsDownOnContextCancel(t *testing.T) {
	log := &syncBuffer{}

	srv, err := server.New(server.Config{
		Addr:            "127.0.0.1:0",
		Version:         "test-version",
		Static:          testTree(),
		DistDir:         t.TempDir(),
		Log:             log,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	address := waitForListenAddress(t, log)

	response, err := http.Get("http://" + address + "/api/version")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after a graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// syncBuffer is a log sink the test can read while the server goroutine writes
// to it. bytes.Buffer alone is not safe for that.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

// waitForListenAddress reads the address out of the startup line, which is the
// only place the port chosen for ":0" is visible.
func waitForListenAddress(t *testing.T, log *syncBuffer) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		const marker = "http://"

		if index := strings.Index(log.String(), marker); index >= 0 {
			rest := log.String()[index+len(marker):]

			return strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("server never reported a listen address")

	return ""
}

// The embedded tree must actually contain what the page asks for; an empty or
// partial embed would otherwise only show up in a browser.
func TestEmbeddedTreeIsComplete(t *testing.T) {
	handler := newTestServerWithRealTree(t).Handler()

	for _, target := range []string{
		"/", "/index.html", "/main.js", "/ui.js", "/wood-texture.js",
		"/styles.css", "/wasm_exec.js", "/assets/glockenmark.svg",
	} {
		response := get(t, handler, target)
		_ = response.Body.Close()

		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", target, response.StatusCode)
		}
	}
}

func newTestServerWithRealTree(t *testing.T) *server.Server {
	t.Helper()

	srv, err := server.New(server.Config{
		Addr:    "127.0.0.1:0",
		Version: "test-version",
		Static:  web.StaticFS(),
		DistDir: t.TempDir(),
		Log:     io.Discard,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	return srv
}

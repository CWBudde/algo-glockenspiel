package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/cwbudde/glockenspiel/internal/server"
	"github.com/cwbudde/glockenspiel/web"
	"github.com/spf13/cobra"
)

type serveOptions struct {
	addr    string
	distDir string
}

func newServeCmd() *cobra.Command {
	options := serveOptions{
		addr:    ":8080",
		distDir: filepath.FromSlash("web/dist"),
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the glockenspiel web app over HTTP",
		Long: "Host the browser front end on a local port. The app is read from disk, out of the " +
			"--dist directory: both the bundle and the WebAssembly module are build artifacts " +
			"that no checkout contains until `just build-web` has produced them. The binary " +
			"carries only a placeholder page naming that command.",
		Example: `  # Serve on the default port
  glockenspiel serve

  # Serve on another port with the app built somewhere else
  glockenspiel serve --addr :9000 --dist ../glockenspiel/web/dist`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.addr, "addr", options.addr, "Listen address, such as :8080 or 127.0.0.1:8080")
	flags.StringVar(&options.distDir, "dist", options.distDir,
		"Directory holding the built web app and the WebAssembly module, relative to the current directory")

	return cmd
}

func runServe(cmd *cobra.Command, options serveOptions) error {
	if options.addr == "" {
		return fmt.Errorf("addr is required")
	}

	httpServer, err := server.New(server.Config{
		Addr:    options.addr,
		Version: version,
		Static:  web.StaticFS(),
		DistDir: options.distDir,
		Log:     cmd.OutOrStdout(),
	})
	if err != nil {
		return err
	}

	// Say this before the port is opened rather than after the first failed
	// fetch: a page that loads its markup and then cannot make a sound is the
	// confusing case this warning exists to prevent. The two halves come from
	// two build steps and either can be missing on its own, so both are
	// checked and both are named.
	if missing := httpServer.MissingAppError(); missing != nil {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), "warning: "+server.MissingAppMessage(options.distDir))
	}

	if missing := httpServer.MissingWasmError(); missing != nil {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), "warning: "+server.MissingWasmMessage(options.distDir))
	}

	// Ctrl-C should end the run cleanly, letting in-flight requests finish,
	// with the same signal plumbing fit uses for interrupting a search.
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return httpServer.Run(ctx)
}

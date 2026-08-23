import { createReadStream, statSync } from "node:fs";
import { resolve } from "node:path";

import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// The build artifacts the page fetches by name rather than importing, so Vite
// never sees them and the dev server would answer 404. scripts/build-wasm.sh
// writes both into web/dist.
const distArtifacts: Record<string, string> = {
  "/glockenspiel.wasm": "application/wasm",
  "/glockenspiel-fit.wasm": "application/wasm",
  "/manifest.json": "application/json",
};

/**
 * serveDistArtifacts hands the dev server the three files that only the
 * production build produces.
 *
 * `npm run dev` serves from web/, but both modules and manifest.json are
 * written to web/dist by scripts/build-wasm.sh, so without this the documented
 * hot-reload workflow cannot instantiate either module however often you build.
 * Copying them into web/ instead would put build output in a source directory;
 * mapping the two URLs costs less and keeps dist the only place they live.
 *
 * A missing file falls through to Vite's own 404, which is the honest answer:
 * `just build-web` has not run yet.
 */
function serveDistArtifacts(): Plugin {
  const dist = resolve(import.meta.dirname, "dist");

  return {
    name: "serve-dist-artifacts",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const url = request.url?.split("?")[0] ?? "";
        const contentType = distArtifacts[url];
        if (contentType === undefined) {
          next();

          return;
        }

        const file = resolve(dist, url.slice(1));

        try {
          statSync(file);
        } catch {
          next();

          return;
        }

        response.setHeader("Content-Type", contentType);
        response.setHeader("Cache-Control", "no-store");
        createReadStream(file).pipe(response);
      });
    },
  };
}

export default defineConfig({
  // Relative asset URLs, so one bundle works both at http://localhost:8080/
  // (glockenspiel serve) and under the project sub-path GitHub Pages hands out
  // (https://cwbudde.github.io/algo-glockenspiel/). An absolute base would work
  // in exactly one of the two.
  base: "./",

  plugins: [react(), serveDistArtifacts()],

  build: {
    outDir: "dist",

    // scripts/build-wasm.sh writes both modules and manifest.json into the same
    // directory. Vite would delete them on every build, and the two steps
    // run in either order depending on what changed, so neither may erase the
    // other's output.
    emptyOutDir: false,
  },

  server: {
    // `npm run dev` serves the app itself but has no fit API; forward /api to a
    // `glockenspiel serve` running beside it so the Optimize tab works in dev.
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
      },
    },
  },
});

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  // Relative asset URLs, so one bundle works both at http://localhost:8080/
  // (glockenspiel serve) and under the project sub-path GitHub Pages hands out
  // (https://cwbudde.github.io/algo-glockenspiel/). An absolute base would work
  // in exactly one of the two.
  base: "./",

  plugins: [react()],

  build: {
    outDir: "dist",

    // scripts/build-wasm.sh writes glockenspiel.wasm and manifest.json into the
    // same directory. Vite would delete them on every build, and the two steps
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

import { readFileSync } from "node:fs"
import type { NextConfig } from "next"

/**
 * The browser always talks to /api on the same origin as the app: the session
 * cookie is HttpOnly and SameSite=Strict, so a separate API host would simply
 * drop it.
 *
 * How /api reaches the Go backend differs by environment:
 *  - development: Next rewrites it, so `bun dev` needs no extra moving parts.
 *  - production: the compose stack puts a reverse proxy in front of both, and
 *    that proxy routes /api to the backend. Next never sees those requests.
 *
 * Rewrites are evaluated at build time, so JD_API_URL is read then — which
 * is exactly why production does not depend on it.
 */
// JD_BACKEND_PORT is honoured as well as JD_API_URL, so a developer who moved
// the backend off 8080 because something else had it does not have to discover
// a second variable to keep `bun dev` working.
const apiPort = process.env.JD_BACKEND_PORT ?? "8080"
const apiTarget = process.env.JD_API_URL ?? `http://127.0.0.1:${apiPort}`

// Work that belongs to a developer, not to an operator's install.
//
// Install and update are both `docker compose up --build`, so every server
// that runs the dashboard also compiles it. `next build` runs tsc over the
// whole project in a second Node process while the compiler still holds its
// own graph, and that overlap — not the compile — is what pushed the image
// build past the memory a 2 GB server has, intermittently, which is worse
// than failing outright. The code being built here was already type-checked
// before it was released; repeating that on every operator's machine buys
// nothing and costs them the install.
//
// Only the Dockerfile sets this. A developer running `bun run build` does not,
// so the gate documented in AGENTS.md still type-checks exactly as before.
const imageBuild = process.env.JD_IMAGE_BUILD === "1"

/**
 * The version of an installed package whose runtime a sync script copies into
 * `public/` under that version (`scripts/sync-monaco.mjs`,
 * `scripts/sync-excalidraw.mjs`). Read here the way the scripts read it, so
 * the folder they wrote is the address the pages are built with and the path
 * the cache rule below is for.
 */
function installed(name: string): string {
  return JSON.parse(readFileSync(`node_modules/${name}/package.json`, "utf8")).version
}
const monaco = `/monaco/${installed("monaco-editor")}`
const excalidraw = `/excalidraw/${installed("@excalidraw/excalidraw")}`

// A year, and never asked about again: only for a path that names its version.
const forGood = [{ key: "Cache-Control", value: "public, max-age=31536000, immutable" }]
// A logo keeps its name when it is redrawn, so it is kept for a day and
// refreshed behind the page for a week after: an update's new mark is on
// screen by the next day at the latest.
const forADay = [
  { key: "Cache-Control", value: "public, max-age=86400, stale-while-revalidate=604800" },
]

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  typescript: { ignoreBuildErrors: imageBuild },
  // Source maps for the prerender pass default on and are only read when a
  // static page throws during the build. That is a developer's concern, and
  // holding them is another few hundred megabytes on a machine that has none.
  enablePrerenderSourceMaps: !imageBuild,
  // This repo keeps its own instructions; the generated ones are noise.
  agentRules: false,
  // Next answers a path that ends in a slash with a 308 to the same path
  // without one, before it looks at the rewrite below. About 140 API calls
  // spell a list endpoint with its slash (`/jobs/`, `/deploy/`), which the
  // backend's router accepts as it is, so wherever Next fronts the API (`bun
  // dev`, and `next start` under the browser specs) each of them was two
  // round trips: ten of the Overview's reads on load. With the redirect off
  // the request reaches the rewrite as it was sent. Pages still lose their
  // slash: `src/proxy.ts` does that redirect itself, for everything but /api.
  skipTrailingSlashRedirect: true,
  // Written into the build: `lib/vendored.ts` hands them to the editor's
  // loader and to Excalidraw.
  env: {
    NEXT_PUBLIC_MONACO_VS: `${monaco}/vs`,
    NEXT_PUBLIC_EXCALIDRAW_ASSETS: `${excalidraw}/`,
  },
  // What is in `public/` has no hash in its name, so Next answers it with
  // `max-age=0` and the browser asks again for every file on every visit:
  // dozens of round trips to open an editor, over the SSH or Tailscale tunnel
  // this product is used through. The two copied runtimes sit under their
  // version and are kept for good; the logos are kept for a day. `src/proxy.ts`
  // leaves the same paths alone, so none of them is given a page's policy and
  // a nonce it has no use for.
  async headers() {
    return [
      { source: `${monaco}/:path*`, headers: forGood },
      { source: `${excalidraw}/fonts/:path*`, headers: forGood },
      { source: "/logos/:path*", headers: forADay },
      { source: "/brand-logo/:path*", headers: forADay },
    ]
  },
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiTarget}/api/:path*` }]
  },
  // The network pages left Security in 0.7.1. Every link inside the product
  // was moved with them; these keep a bookmark working, and keep the query
  // string the firewall's rule hand-off and the tools' hand-off are written
  // in. Not permanent: a 308 is cached by the browser for good, and these
  // paths may mean something again one day.
  async redirects() {
    return [
      { source: "/security/firewall", destination: "/network/firewall", permanent: false },
      { source: "/security/connections", destination: "/network/connections", permanent: false },
      { source: "/security/network", destination: "/network/interfaces", permanent: false },
      { source: "/security/tools", destination: "/network/tools", permanent: false },
      // Boards became Excalidraw, after the editor it is, in 0.7.1.
      { source: "/boards", destination: "/excalidraw", permanent: false },
      { source: "/boards/:id", destination: "/excalidraw/:id", permanent: false },
    ]
  },
}

export default nextConfig

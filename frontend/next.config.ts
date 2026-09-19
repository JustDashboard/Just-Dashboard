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
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiTarget}/api/:path*` }]
  },
}

export default nextConfig

import { expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { COPIED, listenOptions } from "../../scripts/serve-standalone.mjs"

const read = (name) => readFileSync(new URL(`../../${name}`, import.meta.url), "utf8")

test("`bun run start` serves the build with the standalone server the image runs", () => {
  // `next start` reads node_modules and the project's public folder, so a
  // file the standalone trace left out passed every browser spec.
  expect(JSON.parse(read("package.json")).scripts.start).toBe("node scripts/serve-standalone.mjs")
  expect(read("next.config.ts")).toContain('output: "standalone"')
})

test("it copies beside the server exactly what the image copies there", () => {
  const dockerfile = read("Dockerfile")
  expect(dockerfile).toContain("/app/.next/standalone ./")
  for (const [from, to] of COPIED)
    expect(dockerfile).toContain(`/app/${from} ./${to.replace(".next/standalone/", "")}`)
  expect(dockerfile.match(/^COPY --from=build .*$/gm)).toHaveLength(COPIED.length + 1)
  expect(dockerfile).toContain('CMD ["node", "server.js"]')
})

test("the documented commands listen where they did under `next start`", () => {
  expect(listenOptions(["--hostname", "127.0.0.1", "--port", "43117"])).toEqual({
    hostname: "127.0.0.1",
    port: 43117,
  })
  expect(listenOptions(["--port", "43117"])).toEqual({ hostname: "127.0.0.1", port: 43117 })
  expect(listenOptions(["-H", "::1", "-p", "8080"])).toEqual({ hostname: "::1", port: 8080 })
  expect(listenOptions(["--hostname=localhost", "--port=3001"])).toEqual({
    hostname: "localhost",
    port: 3001,
  })
})

test("with no flags it listens on loopback, on PORT or 3000, and never on HOSTNAME", () => {
  expect(listenOptions([])).toEqual({ hostname: "127.0.0.1", port: 3000 })
  expect(listenOptions([], { PORT: "4000", HOSTNAME: "build-runner-7" })).toEqual({
    hostname: "127.0.0.1",
    port: 4000,
  })
})

test("an argument it does not know stops it, as a mistyped port does", () => {
  expect(() => listenOptions(["--keepAliveTimeout", "5"])).toThrow("unknown argument")
  expect(() => listenOptions(["--port"])).toThrow("--port needs a value")
  expect(() => listenOptions(["--port", "http"])).toThrow("is not a port")
  expect(() => listenOptions(["--port", "70000"])).toThrow("is not a port")
})

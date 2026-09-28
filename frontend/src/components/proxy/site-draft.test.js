import { describe, expect, test } from "bun:test"
import {
  changeCount,
  changesDiff,
  draftFate,
  specFromServer,
  droppedCount,
  droppedDiff,
  movableLines,
  sameSpec,
  withMovedLines,
} from "./site-draft"
import { diffRows } from "../files/diff-rows"

const spec = (overrides) => ({
  name: "app.example.com",
  domains: ["app.example.com"],
  kind: "proxy",
  upstream: "http://127.0.0.1:3000",
  tls: true,
  certPath: "/etc/ssl/app.pem",
  keyPath: "/etc/ssl/app.key",
  forceHttps: true,
  hsts: true,
  http2: true,
  webSockets: true,
  gzip: true,
  blockExploits: true,
  securityHeaders: true,
  allowFrom: [],
  denyFrom: [],
  accessLog: true,
  locations: [],
  ...overrides,
})

const dropped = [
  { line: 28, lines: 1, text: "listen 8080;", context: "server", movable: true },
  {
    line: 40,
    lines: 3,
    text: "location ~ \\.php$ {\n    fastcgi_pass unix:/run/php.sock;\n}",
    context: "server",
    movable: true,
  },
  {
    line: 66,
    lines: 1,
    text: "proxy_set_header X-Tenant acme;",
    context: "location /",
    movable: false,
  },
  {
    line: 70,
    lines: 1,
    text: "access_log /var/log/nginx/custom.log;",
    context: "server",
    movable: false,
    reason: "the form writes its own access_log",
  },
]

describe("specFromServer", () => {
  test("a site without an upload limit or a timeout is not given the new-site ones", () => {
    const { clientMaxBody, proxyTimeout, ...read } = spec({ tls: false, hsts: false })
    expect(clientMaxBody).toBeUndefined()
    expect(proxyTimeout).toBeUndefined()
    const form = specFromServer(read)
    expect(form.clientMaxBody).toBeUndefined()
    expect(form.proxyTimeout).toBeUndefined()
    expect(sameSpec(form, read)).toBe(true)
  })
  test("what the file says is kept, and HSTS waits on TLS", () => {
    const form = specFromServer(
      spec({ clientMaxBody: "10m", proxyTimeout: 300, tls: false, hsts: false }),
    )
    expect(form).toMatchObject({ clientMaxBody: "10m", proxyTimeout: 300, hsts: true })
    expect(specFromServer(spec({ hsts: false })).hsts).toBe(false)
  })
})

describe("sameSpec", () => {
  test("a switch the file never mentioned is the same as one set to false", () => {
    const read = spec({ spa: undefined, permanent: undefined, custom: undefined })
    expect(sameSpec(read, spec({ spa: false, custom: "", proxyTimeout: 0 }))).toBe(true)
  })
  test("key order does not count, and a real edit does", () => {
    const { upstream, ...rest } = spec()
    expect(sameSpec(spec(), { upstream, ...rest })).toBe(true)
    expect(sameSpec(spec(), spec({ upstream: "http://127.0.0.1:4000" }))).toBe(false)
    expect(sameSpec(spec(), spec({ http2: false }))).toBe(false)
    expect(sameSpec(spec(), spec({ locations: [{ path: "/api", upstream: "http://x:1" }] }))).toBe(
      false,
    )
  })
  test("compared as sent: HSTS means nothing without TLS", () => {
    expect(sameSpec(spec({ tls: false, hsts: true }), spec({ tls: false, hsts: false }))).toBe(true)
  })
})

describe("draftFate", () => {
  const base = { digest: "d1", spec: spec() }
  test("no draft yet takes the file", () => {
    expect(draftFate(null, spec(), "d1")).toBe("take")
  })
  test("a draft of the file as it still is survives", () => {
    expect(draftFate(base, spec({ upstream: "http://127.0.0.1:4000" }), "d1")).toBe("keep")
  })
  test("a file changed under a draft with no edits is taken, and under one with edits asked about", () => {
    expect(draftFate(base, spec(), "d2")).toBe("take")
    expect(draftFate(base, spec({ upstream: "http://127.0.0.1:4000" }), "d2")).toBe("stale")
  })
})

describe("droppedDiff", () => {
  test("each dropped line sits at its own line of the file, under where it is", () => {
    const rows = diffRows(droppedDiff(dropped), true)
    const removed = rows.filter((r) => r.kind === "del")
    expect(removed.map((r) => [r.oldNo, r.text])).toEqual([
      [28, "-listen 8080;"],
      [40, "-location ~ \\.php$ {"],
      [41, "-    fastcgi_pass unix:/run/php.sock;"],
      [42, "-}"],
      [66, "-proxy_set_header X-Tenant acme;"],
      [70, "-access_log /var/log/nginx/custom.log;"],
    ])
    const hunks = rows.filter((r) => r.kind === "hunk").map((r) => r.text)
    expect(hunks).toEqual([
      "@@ -28,1 +28,0 @@ server",
      "@@ -40,3 +40,0 @@ server",
      "@@ -66,1 +66,0 @@ location /",
      "@@ -70,1 +70,0 @@ server",
    ])
  })
  test("neighbours in the same block are one run", () => {
    const next = [
      { line: 28, lines: 1, text: "listen 8080;", context: "server", movable: true },
      { line: 29, lines: 1, text: "client_body_buffer_size 1m;", context: "server", movable: true },
      {
        line: 30,
        lines: 1,
        text: "proxy_set_header X-A 1;",
        context: "location /",
        movable: false,
      },
    ]
    expect(droppedDiff(next)).toBe(
      "@@ -28,2 +28,0 @@ server\n-listen 8080;\n-client_body_buffer_size 1m;\n@@ -30,1 +30,0 @@ location /\n-proxy_set_header X-A 1;",
    )
  })
  test("counts lines, not statements", () => {
    expect(droppedCount(dropped)).toBe(6)
  })
})

describe("moving lines into the extra configuration", () => {
  test("only the movable ones go, after what is there", () => {
    expect(withMovedLines("# mine\n", dropped)).toBe(
      "# mine\nlisten 8080;\nlocation ~ \\.php$ {\n    fastcgi_pass unix:/run/php.sock;\n}",
    )
    expect(withMovedLines(undefined, dropped.slice(0, 1))).toBe("listen 8080;")
  })
  test("a line already moved is not moved again", () => {
    const once = withMovedLines("", dropped)
    expect(movableLines(dropped, once)).toEqual([])
    expect(withMovedLines(once, dropped)).toBe(once)
  })
})

describe("changesDiff", () => {
  const disk = "server {\n    listen 443 ssl;\n    http2 on;\n    server_name a;\n}\n"
  test("a line the save leaves out is a removed line, with its number", () => {
    const next = disk.replace("    http2 on;\n", "")
    const diff = changesDiff(disk, next, "app")
    const rows = diffRows(diff, true)
    expect(rows.find((r) => r.kind === "del")).toEqual({
      kind: "del",
      text: "-    http2 on;",
      oldNo: 3,
    })
    // The file header is not drawn as a removed and an added line.
    expect(rows.some((r) => r.text.startsWith("---") || r.text.startsWith("+++"))).toBe(false)
    expect(changeCount(diff)).toBe(1)
  })
  test("nothing to change is empty", () => {
    expect(changesDiff(disk, disk, "app")).toBe("")
    expect(changeCount("")).toBe(0)
  })
})

import { describe, expect, test } from "bun:test"
import {
  composeOutline,
  configHues,
  diffCounts,
  diffHunks,
  doneWord,
  errorLine,
  fileRoles,
  historySpans,
  lineDiff,
  outlineAt,
  sameFile,
  shortDigest,
  splitImage,
} from "./stack-views"

const COMPOSE = `name: shop

services:
  web:
    image: nginx:1.27-alpine
    volumes:
      - ./nginx:/etc/nginx/conf.d:ro
      - static:/srv/static
  api:
    build: ./app
    env_file: .env
    healthcheck:
      test: ["CMD", "true"]
  worker:
    build:
      context: ./worker
      dockerfile: Dockerfile.prod
    env_file:
      - .env
      - path: ./worker.env
    volumes:
    - type: bind
      source: ./data/queue
      target: /queue
    - type: volume
      source: cache
      target: /cache

networks:
  frontend:

volumes:
  static:
  cache:
`

describe("composeOutline", () => {
  test("lists the top-level keys and the names directly under each, with their lines", () => {
    const outline = composeOutline(COMPOSE)
    expect(outline.map((s) => [s.key, s.line])).toEqual([
      ["name", 1],
      ["services", 3],
      ["networks", 29],
      ["volumes", 32],
    ])
    expect(outline[1].children).toEqual([
      { key: "web", line: 4 },
      { key: "api", line: 9 },
      { key: "worker", line: 14 },
    ])
    expect(outline[3].children.map((c) => c.key)).toEqual(["static", "cache"])
  })

  test("a service's own keys are never read as services, at any indentation", () => {
    const four = "services:\n    db:\n        image: postgres\n    cache:\n        image: redis\n"
    expect(composeOutline(four)[0].children.map((c) => c.key)).toEqual(["db", "cache"])
  })

  test("skips comments, blank lines and the document marker", () => {
    const text = "---\n# the stack\nservices:\n  # a comment\n  web:\n    image: x\n"
    expect(composeOutline(text)).toEqual([
      { key: "services", line: 3, children: [{ key: "web", line: 5 }] },
    ])
  })

  test("outlineAt names the section and service a line is in", () => {
    const outline = composeOutline(COMPOSE)
    expect(outlineAt(outline, 11)).toEqual({ section: "services", child: "api" })
    expect(outlineAt(outline, 3)).toEqual({ section: "services", child: undefined })
    expect(outlineAt(outline, 30)).toEqual({ section: "networks", child: "frontend" })
  })
})

test("errorLine reads the line compose stopped on", () => {
  expect(errorLine("yaml: line 12: did not find expected key")).toBe(12)
  expect(errorLine("services.web Additional property foo is not allowed")).toBeUndefined()
  expect(errorLine(undefined)).toBeUndefined()
})

describe("lineDiff", () => {
  test("marks what was removed and added, grouped by the service each line is under", () => {
    const before = "services:\n  api:\n    image: node:20\n  db:\n    image: postgres\n"
    const after = "services:\n  api:\n    image: node:22\n  db:\n    image: postgres\n"
    const diff = lineDiff(before, after)
    expect(diff.filter((l) => l.kind !== "same")).toEqual([
      { kind: "removed", text: "    image: node:20", section: "api" },
      { kind: "added", text: "    image: node:22", section: "api" },
    ])
    expect(diffCounts(diff)).toEqual({ added: 1, removed: 1 })
  })

  test("keeps three lines around a change and leaves a gap for the rest", () => {
    const lines = Array.from({ length: 20 }, (_, i) => `line ${i}`)
    const after = [...lines]
    after[2] = "changed early"
    after[17] = "changed late"
    const diff = lineDiff(lines.join("\n"), after.join("\n"))
    expect(diff.filter((l) => l.kind === "gap")).toHaveLength(1)
    expect(diff[0]).toEqual({ kind: "same", text: "line 0", section: "" })
    expect(diff.at(-1)).toEqual({ kind: "same", text: "line 19", section: "" })
  })

  test("a change to a service's own key line is that service's, comment or not", () => {
    const before = "services:\n  web:\n    image: nginx\n  db:\n    image: postgres\n"
    const after = "services:\n  web:\n    image: nginx\n  db:  # pinned\n    image: postgres\n"
    const hunks = diffHunks(lineDiff(before, after))
    expect(hunks.map((h) => h.section)).toEqual(["db"])
    expect(lineDiff(before, after).at(-1)).toEqual({
      kind: "same",
      text: "    image: postgres",
      section: "db",
    })
  })

  test("two identical files have no diff, whatever whitespace trails them", () => {
    expect(lineDiff("a: 1\nb: 2\n", "a: 1\nb: 2\n\n\n")).toEqual([])
  })

  test("diffHunks heads each run between gaps with the service its first change is under", () => {
    const before = COMPOSE
    const after = COMPOSE.replace("nginx:1.27-alpine", "nginx:1.28-alpine").replace(
      "path: ./worker.env",
      "path: ./worker.prod.env",
    )
    const hunks = diffHunks(lineDiff(before, after))
    expect(hunks.map((h) => [h.section, h.added, h.removed])).toEqual([
      ["web", 1, 1],
      ["worker", 1, 1],
    ])
  })
})

test("splitImage finds the tag past a registry's port", () => {
  expect(splitImage("python:3.12-slim")).toEqual({ repo: "python", tag: "3.12-slim" })
  expect(splitImage("registry:5000/team/app")).toEqual({
    repo: "registry:5000/team/app",
    tag: "latest",
  })
  expect(splitImage("registry:5000/team/app:v2")).toEqual({
    repo: "registry:5000/team/app",
    tag: "v2",
  })
})

describe("fileRoles", () => {
  test("reads build contexts, bind mounts and env files in their short and long forms", () => {
    expect(fileRoles(COMPOSE)).toEqual([
      { path: "nginx", service: "web", role: "mount", target: "/etc/nginx/conf.d" },
      { path: "app", service: "api", role: "build" },
      { path: ".env", service: "api", role: "env" },
      { path: "worker", service: "worker", role: "build" },
      { path: ".env", service: "worker", role: "env" },
      { path: "worker.env", service: "worker", role: "env" },
      { path: "data/queue", service: "worker", role: "mount", target: "/queue" },
    ])
  })

  test("leaves named volumes, absolute paths and paths outside the directory unsaid", () => {
    const text = `services:
  db:
    volumes:
      - pgdata:/var/lib/postgresql/data
      - /etc/ssl:/etc/ssl:ro
      - ../shared:/shared
    build: .
`
    expect(fileRoles(text)).toEqual([{ path: "", service: "db", role: "build" }])
  })

  test("reads inline lists", () => {
    const text = `services:
  web:
    volumes: ["./site:/usr/share/nginx/html"]
    env_file: [.env, ./web.env]
`
    expect(fileRoles(text)).toEqual([
      { path: "site", service: "web", role: "mount", target: "/usr/share/nginx/html" },
      { path: ".env", service: "web", role: "env" },
      { path: "web.env", service: "web", role: "env" },
    ])
  })
})

describe("history", () => {
  const record = (id, minutesAgo, hash, action = "up") => ({
    id,
    project: "shop",
    createdAt: new Date(Date.UTC(2026, 9, 8, 12) - minutesAgo * 60_000).toISOString(),
    configHash: hash,
    services: [],
    imageDigests: {},
    action,
  })
  const NOW = Date.UTC(2026, 9, 8, 12)

  test("historySpans lays the records oldest first, each to the next and the newest to now", () => {
    const spans = historySpans([record(3, 25, "b"), record(2, 50, "b"), record(1, 100, "a")], NOW)
    expect(spans.map((s) => s.id)).toEqual([1, 2, 3])
    expect(spans.map((s) => [s.start, s.width])).toEqual([
      [0, 0.5],
      [0.5, 0.25],
      [0.75, 0.25],
    ])
    expect(spans.map((s) => s.changed)).toEqual([false, true, false])
  })

  test("configHues gives each file its own lane, oldest first", () => {
    const hues = configHues([
      record(3, 25, "c"),
      record(2, 50, "b"),
      record(1, 100, "a"),
      record(0, 200, "b"),
    ])
    expect([...hues.keys()]).toEqual(["b", "a", "c"])
    expect(new Set(hues.values()).size).toBe(3)
  })

  test("no records, no spans", () => {
    expect(historySpans([], NOW)).toEqual([])
  })

  test("doneWord says what each action did", () => {
    expect(doneWord("up")).toBe("Deployed")
    expect(doneWord("update")).toBe("Pulled and redeployed")
    expect(doneWord(undefined)).toBe("Deployed")
    expect(doneWord("build")).toBe("Build")
  })

  test("sameFile ignores trailing whitespace, as the server's hash does", () => {
    expect(sameFile("a: 1\n", "a: 1")).toBe(true)
    expect(sameFile("a: 1", "a: 2")).toBe(false)
    expect(sameFile(undefined, "a: 1")).toBe(false)
  })

  test("shortDigest keeps the repository and the hash's head and tail", () => {
    expect(
      shortDigest("nginx@sha256:5f0574409b3add89581b96c68afe9e9c7b284651c3a974b6e8bac46bf95e6b7f"),
    ).toEqual({ repo: "nginx", hash: "5f0574409b3a…6b7f" })
    expect(shortDigest("sha256:abc")).toEqual({ repo: "", hash: "abc" })
  })
})

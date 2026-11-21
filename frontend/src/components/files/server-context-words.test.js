import { describe, expect, test } from "bun:test"
import {
  agoWords,
  daysLeftWords,
  entryFacts,
  factProduct,
  partialSentence,
  phrase,
  rowFact,
  scheduleWords,
  sentence,
} from "./server-context-words"

const NOW = Date.parse("2026-11-20T12:00:00Z")
const hoursAgo = (n) => new Date(NOW - n * 3_600_000).toISOString()
const daysAhead = (n) => new Date(NOW + n * 86_400_000 + 3_600_000).toISOString()

const volume = {
  kind: "volume",
  source: "docker",
  names: ["shop_pgdata", "shop-db"],
  image: "postgres:16",
  href: "/docker/volumes/shop_pgdata",
  inherits: true,
}
const nightly = {
  kind: "backup",
  source: "backups",
  names: ["srv nightly"],
  target: "b2",
  schedule: "0 3 * * *",
  state: "ok",
  at: hoursAgo(3),
  href: "/backups/1",
  inherits: true,
}

describe("each fact as a row says it", () => {
  test("the operator's examples", () => {
    expect(phrase(volume, NOW)).toBe("Postgres volume of shop-db")
    expect(
      phrase(
        { kind: "bind", source: "docker", names: ["api", "/app/uploads"], image: "node:22" },
        NOW,
      ),
    ).toBe("Bind mount of api")
    expect(phrase({ kind: "compose-file", source: "docker", names: ["shop"] }, NOW)).toBe(
      "The stack shop's Compose file",
    )
    expect(
      phrase({ kind: "site-root", source: "proxy", names: ["example.com"], product: "nginx" }, NOW),
    ).toBe("Served at example.com by nginx")
    expect(
      phrase(
        { kind: "certificate", source: "proxy", names: ["example.com"], until: daysAhead(61) },
        NOW,
      ),
    ).toBe("Certificate for example.com, 61 days left")
    expect(phrase(nightly, NOW)).toBe("Backed up nightly to B2")
    expect(
      phrase(
        { kind: "git", source: "git", names: ["app"], detail: "main", count: 3, state: "changed" },
        NOW,
      ),
    ).toBe("Git checkout on main, 3 uncommitted")
    expect(phrase({ kind: "deployment-source", source: "deploy", names: ["shop"] }, NOW)).toBe(
      "The deployment shop's source",
    )
    expect(
      phrase(
        {
          kind: "database-data",
          source: "databases",
          names: ["main"],
          product: "postgresql",
          detail: "16",
        },
        NOW,
      ),
    ).toBe("Data directory of PostgreSQL 16")
    expect(phrase({ kind: "unit-file", source: "processes", names: ["nginx.service"] }, NOW)).toBe(
      "Unit file of nginx.service",
    )
    expect(phrase({ kind: "pm2", source: "processes", names: ["api"] }, NOW)).toBe(
      "PM2 app api runs here",
    )
    expect(phrase({ kind: "dashboard-data", source: "dashboard", names: [] }, NOW)).toBe(
      "Just Dashboard's own data",
    )
  })

  test("a clean checkout and one in conflict", () => {
    const clean = { kind: "git", source: "git", names: ["app"], detail: "main", count: 0 }
    expect(phrase(clean, NOW)).toBe("Git checkout on main")
    expect(phrase({ ...clean, state: "conflicts", count: 2 }, NOW)).toBe(
      "Git checkout on main, conflicts to resolve",
    )
    expect(phrase({ ...clean, detail: "detached at 1a2b3c4" }, NOW)).toBe(
      "Git checkout detached at 1a2b3c4",
    )
  })

  test("a volume nothing uses says so", () => {
    expect(phrase({ ...volume, names: ["orphan"] }, NOW)).toBe("Docker volume orphan, unused")
  })
})

describe("each fact as the details pane says it", () => {
  test("a backup says when it last ran, or that it failed", () => {
    expect(sentence(nightly, NOW)).toBe(
      "Backed up nightly to B2 by srv nightly, last run 3 hours ago",
    )
    expect(sentence({ ...nightly, state: "failed" }, NOW)).toBe(
      "Backed up nightly to B2 by srv nightly, last run failed",
    )
    expect(sentence({ ...nightly, state: "paused" }, NOW)).toBe(
      "The backup srv nightly would cover it, but it is paused",
    )
  })

  test("an inherited fact says what the entry is inside", () => {
    expect(sentence({ ...volume, from: "/var/lib/docker/volumes/shop_pgdata/_data" }, NOW)).toBe(
      "Inside the volume of shop-db",
    )
    expect(sentence({ ...nightly, from: "/srv" }, NOW)).toBe(
      "Covered by the nightly backup of /srv to B2, last run 3 hours ago",
    )
  })

  test("not backed up, and left out on purpose", () => {
    expect(sentence({ kind: "not-backed-up", source: "backups", names: [] }, NOW)).toBe(
      "Not backed up: no backup job covers it",
    )
    expect(
      sentence({ kind: "backup-excluded", source: "backups", names: ["srv nightly"] }, NOW),
    ).toBe("Left out of the backup srv nightly on purpose")
  })

  test("several containers and several domains are listed, then counted", () => {
    expect(sentence({ ...volume, names: ["data", "a", "b", "c", "d"] }, NOW)).toBe(
      "Postgres volume of a, b, c and 1 more, Docker volume data",
    )
    expect(
      sentence(
        {
          kind: "site-root",
          source: "proxy",
          names: ["a.test", "b.test", "c.test"],
          product: "caddy",
        },
        NOW,
      ),
    ).toBe("Served at a.test, b.test and 1 more by Caddy")
  })
})

describe("which fact a row says", () => {
  test("the most telling own fact, never not-backed-up or an inherited one", () => {
    const git = { kind: "git", source: "git", names: ["app"], detail: "main" }
    const deploy = { kind: "deployment-source", source: "deploy", names: ["shop"] }
    expect(rowFact([git, deploy])).toBe(deploy)
    expect(rowFact([{ kind: "not-backed-up", source: "backups", names: [] }])).toBeUndefined()
    expect(rowFact([{ ...volume, from: "/x" }])).toBeUndefined()
    expect(rowFact(undefined)).toBeUndefined()
  })
})

describe("what an entry inherits from the folder on screen", () => {
  const context = {
    path: "/srv/shop",
    facts: [
      { kind: "compose", source: "docker", names: ["shop"], inherits: true },
      { ...nightly, from: "/srv" },
      { kind: "not-backed-up", source: "backups", names: [] },
    ],
    children: {
      "/srv/shop/cache": [{ ...nightly, kind: "backup-excluded", inherits: true }],
    },
    partial: [],
    checkedAt: new Date(NOW).toISOString(),
  }

  test("the folder is its own facts", () => {
    expect(entryFacts(context, "/srv/shop")).toBe(context.facts)
  })

  test("a child inherits what passes inside, from where it comes", () => {
    const facts = entryFacts(context, "/srv/shop/README.md")
    expect(facts.map((f) => [f.kind, f.from])).toEqual([
      ["compose", "/srv/shop"],
      ["backup", "/srv"],
    ])
  })

  test("a child left out of the backup does not inherit it", () => {
    const facts = entryFacts(context, "/srv/shop/cache")
    expect(facts.map((f) => f.kind)).toEqual(["backup-excluded", "compose"])
  })

  test("no answer is no facts", () => {
    expect(entryFacts(null, "/srv")).toEqual([])
  })
})

describe("words for time and schedules", () => {
  test("ages", () => {
    expect(agoWords(new Date(NOW - 10_000).toISOString(), NOW)).toBe("just now")
    expect(agoWords(new Date(NOW - 60_000).toISOString(), NOW)).toBe("a minute ago")
    expect(agoWords(hoursAgo(1), NOW)).toBe("an hour ago")
    expect(agoWords(hoursAgo(50), NOW)).toBe("2 days ago")
  })

  test("a certificate's time left", () => {
    expect(daysLeftWords(daysAhead(1), NOW)).toBe("1 day left")
    expect(daysLeftWords(new Date(NOW + 3_600_000).toISOString(), NOW)).toBe("expires today")
    expect(daysLeftWords(new Date(NOW - 3 * 86_400_000).toISOString(), NOW)).toBe(
      "expired 3 days ago",
    )
  })

  test("a schedule as an adverb", () => {
    expect(scheduleWords("0 3 * * *")).toBe("nightly")
    expect(scheduleWords("30 14 * * *")).toBe("daily")
    expect(scheduleWords("0 * * * *")).toBe("hourly")
    expect(scheduleWords("0 4 * * 0")).toBe("weekly")
    expect(scheduleWords("")).toBe("by hand")
  })

  test("sources still being read", () => {
    expect(partialSentence([])).toBeUndefined()
    expect(partialSentence(["git", "docker"])).toBe("Still reading Git and Docker")
  })
})

describe("the logo a fact is drawn with", () => {
  test("a container's image, the backup's destination, the dashboard's own mark", () => {
    expect(factProduct(volume)).not.toBe("docker")
    expect(factProduct({ ...volume, image: undefined })).toBe("docker")
    expect(factProduct(nightly)).toBe("backblaze")
    expect(factProduct({ ...nightly, target: "local" })).toBeUndefined()
    expect(factProduct({ kind: "dashboard-data", source: "dashboard", names: [] })).toBe("jd")
    expect(
      factProduct({ kind: "certificate", source: "proxy", names: [], detail: "Let's Encrypt" }),
    ).toBe("lets-encrypt")
  })
})

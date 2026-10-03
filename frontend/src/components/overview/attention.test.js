import { describe, expect, test } from "bun:test"
import { serverAttention, verdictWith, worstFirst } from "./attention"

const hoursAgo = (hours) => new Date(Date.now() - hours * 3_600_000).toISOString()

const job = (overrides) => ({
  id: 1,
  name: "Nightly",
  enabled: true,
  overdue: false,
  lastRun: { id: 9, jobId: 1, startedAt: hoursAgo(3), status: "success" },
  ...overrides,
})

const cert = (overrides) => ({
  name: "shop",
  domains: ["shop.example.test"],
  daysLeft: 60,
  expired: false,
  expiring: false,
  ...overrides,
})

const updates = (overrides) => ({
  available: true,
  manager: "apt",
  packages: [{}, {}, {}],
  securityCount: 0,
  securityFiltering: true,
  rebootRequired: false,
  ...overrides,
})

const ids = (findings) => findings.map((finding) => finding.id)

describe("serverAttention", () => {
  test("a server with nothing wrong anywhere has nothing to say", () => {
    expect(
      serverAttention({
        deployments: [],
        backups: [job()],
        certificates: [cert()],
        packages: updates(),
        databases: [{ id: 1, name: "shop" }],
        exposure: { grade: "tailscale" },
      }),
    ).toEqual([])
  })

  test("modules that have not answered yet add nothing", () => {
    expect(serverAttention({})).toEqual([])
  })

  test("a failed backup is critical and opens its job", () => {
    const [finding] = serverAttention({
      backups: [job({ id: 4, lastRun: { startedAt: hoursAgo(2), status: "failed" } })],
    })
    expect(finding).toMatchObject({
      id: "backup-4",
      level: "critical",
      action: { href: "/backups/4" },
    })
    expect(finding.title).toStartWith("Nightly: backup failed")
  })

  test("a quiet backup is a warning, and a paused job is not news", () => {
    expect(serverAttention({ backups: [job({ overdue: true })] })).toMatchObject([
      { id: "backup-1", level: "warning", meta: "backup overdue" },
    ])
    expect(
      serverAttention({
        backups: [job({ enabled: false, lastRun: { startedAt: hoursAgo(1), status: "failed" } })],
      }),
    ).toEqual([])
  })

  test("certificates are one finding per kind, naming every domain in it", () => {
    const findings = serverAttention({
      certificates: [
        cert({ domains: ["a.test"], expired: true, daysLeft: -2 }),
        cert({ domains: ["b.test"], expiring: true, daysLeft: 9 }),
        cert({ domains: ["c.test"], expiring: true, daysLeft: 4 }),
      ],
    })
    expect(ids(findings)).toEqual(["certificates-expired", "certificates-renewal"])
    expect(findings[0].title).toBe("a.test: certificate expired")
    expect(findings[1].title).toBe("2 certificates are past their renewal")
    expect(findings[1].detail).toContain("b.test, c.test")
  })

  test("one certificate past its renewal says when it runs out", () => {
    const [finding] = serverAttention({
      certificates: [cert({ domains: ["status.test"], expiring: true, daysLeft: 12 })],
    })
    expect(finding.title).toBe("status.test: certificate expires in 12d")
  })

  test("security updates and an owed reboot are each a finding", () => {
    expect(
      ids(serverAttention({ packages: updates({ securityCount: 2, rebootRequired: true }) })),
    ).toEqual(["packages-security", "packages-reboot"])
    expect(serverAttention({ packages: updates({ available: false, securityCount: 2 }) })).toEqual(
      [],
    )
  })

  test("broken saved databases are named with their reason", () => {
    const [finding] = serverAttention({
      databases: [
        { id: 1, name: "shop" },
        { id: 2, name: "blog", broken: true, brokenReason: "container removed" },
      ],
    })
    expect(finding).toMatchObject({ id: "databases-broken", level: "warning" })
    expect(finding.detail).toBe("blog: container removed")
  })

  test("an open dashboard is critical, a public one a warning", () => {
    expect(serverAttention({ exposure: { grade: "open", summary: "" } })[0].level).toBe("critical")
    expect(serverAttention({ exposure: { grade: "public", summary: "" } })[0].level).toBe("warning")
  })

  test("the fleet's findings come through in the fleet's own words", () => {
    const [finding] = serverAttention({
      deployments: [
        {
          id: 3,
          name: "docs",
          liveReleaseId: 10,
          health: "unhealthy",
          lastRun: { id: 5, state: "succeeded", requestedAt: hoursAgo(1) },
        },
      ],
    })
    expect(finding).toMatchObject({
      id: "project-3",
      level: "critical",
      action: { href: "/deploy/3/runtime" },
    })
  })

  test("worst first across every module", () => {
    const findings = serverAttention({
      packages: updates({ securityCount: 1 }),
      backups: [job({ lastRun: { startedAt: hoursAgo(1), status: "failed" } })],
    })
    expect(ids(findings)).toEqual(["backup-1", "packages-security"])
  })
})

describe("worstFirst", () => {
  test("keeps each source's order within a level", () => {
    const findings = [
      { id: "a", level: "notice" },
      { id: "b", level: "warning" },
      { id: "c", level: "critical" },
      { id: "d", level: "warning" },
    ]
    expect(ids(worstFirst(findings))).toEqual(["c", "b", "d", "a"])
  })
})

describe("verdictWith", () => {
  test("raises the recorder's verdict to the worst finding", () => {
    expect(verdictWith("ok", [{ level: "warning" }])).toBe("warning")
    expect(verdictWith("warning", [{ level: "notice" }])).toBe("warning")
    expect(verdictWith("notice", [{ level: "critical" }, { level: "warning" }])).toBe("critical")
    expect(verdictWith("ok", [])).toBe("ok")
  })
})

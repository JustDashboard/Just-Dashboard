import { describe, expect, test } from "bun:test"
import {
  estimateNote,
  keepingSentence,
  scrapeConfig,
  shortens,
  storedSentence,
  windowEstimate,
} from "./metrics-export"

const report = {
  retentionDays: 14,
  options: [
    { days: 7, estimatedBytes: 1_048_576 },
    { days: 14, estimatedBytes: 1_572_864 },
    { days: 30, estimatedBytes: null },
  ],
  connections: 2,
  samples: 1,
  storedBytes: 2048,
  everySeconds: 30,
  rawDays: 7,
  coarseMinutes: 5,
  endpoint: "/api/v1/metrics/databases",
}

describe("scrapeConfig", () => {
  test("names the dashboard's own scheme, host and endpoint", () => {
    const config = scrapeConfig("https://dash.example.com", "/api/v1/metrics/databases")
    expect(config).toContain("scheme: https")
    expect(config).toContain("metrics_path: /api/v1/metrics/databases")
    expect(config).toContain('targets: ["dash.example.com"]')
    expect(config).toContain("credentials: <metrics key>")
  })

  test("keeps a port, and plain http for a dashboard served without TLS", () => {
    const config = scrapeConfig("http://10.0.0.4:8443", "/api/v1/metrics/databases", "vpsd_abc")
    expect(config).toContain("scheme: http")
    expect(config).toContain('targets: ["10.0.0.4:8443"]')
    expect(config).toContain("credentials: vpsd_abc")
  })
})

describe("retention sentences", () => {
  test("only a shorter window deletes history", () => {
    expect(shortens(report, 7)).toBe(true)
    expect(shortens(report, 14)).toBe(false)
    expect(shortens(report, 90)).toBe(false)
  })

  test("an estimate is the server's, and absent until it can measure", () => {
    expect(windowEstimate(report, 14)).toBe(1_572_864)
    expect(windowEstimate(report, 30)).toBeNull()
    expect(windowEstimate(report, 90)).toBeNull()
  })

  test("what is stored is said once, or that there is nothing", () => {
    expect(storedSentence(report)).toBe("About 2.0 KB in 1 saved reading so far.")
    expect(storedSentence({ ...report, storedBytes: null, samples: 0 })).toBe(
      "Nothing is recorded yet.",
    )
  })

  test("the thinning rule uses the server's numbers", () => {
    expect(keepingSentence(report)).toBe(
      "Readings are kept whole for 7 days. Older ones are thinned to the last of every 5 minutes.",
    )
  })

  test("the estimate says what it stands on, or that it has nothing yet", () => {
    expect(estimateNote(report)).toBe(
      "Sizes are estimated for 2 servers at the average reading saved so far.",
    )
    expect(estimateNote({ connections: 1 })).toContain("for 1 server at")
    expect(estimateNote({ connections: 0 })).toBe(
      "The space each window takes appears once readings are saved.",
    )
  })
})

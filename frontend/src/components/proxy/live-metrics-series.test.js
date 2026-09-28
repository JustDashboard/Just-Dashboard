import { describe, expect, test } from "bun:test"
import {
  connectionStates,
  connectionTrend,
  mergeSeries,
  metricsCursor,
  perSecond,
  requestTrend,
  statusAddress,
  readingsStopped,
  windowLabel,
} from "./live-metrics-series"

const start = Date.parse("2026-09-28T12:00:00Z")

function sample(seq, extra = {}) {
  return {
    seq,
    at: new Date(start + (seq - 1) * 5_000).toISOString(),
    active: seq,
    reading: 0,
    writing: 1,
    waiting: 2,
    requests: seq === 1 ? null : seq / 2,
    dropped: 0,
    ...extra,
  }
}

function report(epoch, samples, extra = {}) {
  return {
    supported: true,
    enabled: true,
    path: "/etc/nginx/conf.d/jd-status.conf",
    endpoint: "http://127.0.0.1:19081/jd-status",
    interval: 5,
    epoch,
    samples,
    at: samples.at(-1)?.at ?? new Date(start).toISOString(),
    hourRequests: 0,
    hourDropped: 0,
    ...extra,
  }
}

describe("the readings the overview holds", () => {
  test("the first report is the series, and the cursor asks for what follows it", () => {
    const series = mergeSeries(undefined, report(7, [sample(1), sample(2)]))
    expect(series.epoch).toBe(7)
    expect(series.samples.map((s) => s.seq)).toEqual([1, 2])
    expect(metricsCursor(series)).toEqual({ epoch: 7, after: 2 })
  })

  test("with nothing held there is no cursor, so the whole hour is asked for", () => {
    expect(metricsCursor(undefined)).toBeUndefined()
    expect(metricsCursor({ epoch: 7, samples: [] })).toBeUndefined()
  })

  test("a report of the same series adds what is new and nothing twice", () => {
    const held = mergeSeries(undefined, report(7, [sample(1), sample(2)]))
    const next = mergeSeries(held, report(7, [sample(2), sample(3)]))
    expect(next.samples.map((s) => s.seq)).toEqual([1, 2, 3])
  })

  // Switched off and on, a moved port or a restarted dashboard: the old
  // readings were of something else.
  test("another epoch replaces what is held", () => {
    const held = mergeSeries(undefined, report(7, [sample(1), sample(2)]))
    const again = mergeSeries(held, report(9, [sample(1)]))
    expect(again).toEqual({ epoch: 9, samples: [sample(1)] })
    expect(mergeSeries(held, report(9, []))).toEqual({ epoch: 9, samples: [] })
  })

  test("readings older than an hour before the newest drop off", () => {
    const hour = Array.from({ length: 730 }, (_, i) => sample(i + 1))
    const series = mergeSeries(undefined, report(7, hour))
    expect(series.samples[0].seq).toBe(10)
    expect(Date.parse(series.samples.at(-1).at) - Date.parse(series.samples[0].at)).toBe(3_600_000)
  })
})

describe("what the tiles draw", () => {
  test("a reading with no rate is left out of the line, not drawn at zero", () => {
    const samples = [sample(1), sample(2), sample(3, { requests: null }), sample(4)]
    expect(requestTrend(samples)).toEqual([1, 2])
    expect(connectionTrend(samples)).toEqual([1, 2, 3, 4])
  })

  test("a rate reads in tenths below ten a second and whole above", () => {
    expect(perSecond(0)).toBe("0")
    expect(perSecond(0.4)).toBe("0.4")
    expect(perSecond(9.84)).toBe("9.8")
    expect(perSecond(12.6)).toBe("13")
    expect(perSecond(1234.4)).toBe((1234).toLocaleString())
  })

  test("the hour's total says how far back it reaches until it spans the hour", () => {
    const early = [sample(1), sample(13)]
    expect(windowLabel(early)).toMatch(/^since \d{2}:\d{2}$/)
    expect(windowLabel([sample(1), sample(721)])).toBe("in the last hour")
    expect(windowLabel([])).toBe("")
  })

  test("the connections' states and the address read as nginx names them", () => {
    expect(connectionStates(sample(1, { reading: 6, writing: 179, waiting: 106 }))).toBe(
      "6 reading · 179 writing · 106 idle",
    )
    expect(statusAddress(report(7, []))).toBe("127.0.0.1:19081/jd-status")
  })

  test("readings that stopped say since when and why", () => {
    expect(readingsStopped(report(7, []))).toBeUndefined()
    const failing = report(7, [], {
      error: "nothing is listening on 127.0.0.1:19081",
      failingSince: "2026-09-28T12:00:00Z",
    })
    expect(readingsStopped(failing)).toMatch(
      /^No answer since .+: nothing is listening on 127\.0\.0\.1:19081$/,
    )
    expect(readingsStopped(report(7, [], { error: "timed out" }))).toBe("No answer: timed out")
  })
})

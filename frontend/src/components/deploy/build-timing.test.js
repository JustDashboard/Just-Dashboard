import { describe, expect, test } from "bun:test"
import { buildTimingOf, buildTimingSentence, usualRunSeconds } from "./build-timing"

const MB = 1024 * 1024

describe("buildTimingSentence", () => {
  test("says where a build's time went in one sentence", () => {
    expect(
      buildTimingSentence(
        {
          installSeconds: 5.5,
          compileSeconds: 25.4,
          exportSeconds: 45.2,
          unpackSeconds: 15.4,
          imageSeconds: 61,
          steps: 14,
          cachedSteps: 9,
          imageBytes: 865 * MB,
        },
        107,
      ),
    ).toBe(
      "Build 1m 47s: install 6s, compile 25s, image export 1m 1s for 865 MB; 9 of 14 steps cached.",
    )
  })

  test("a part the cache answered says so, and a sub-second export is not 0s", () => {
    expect(
      buildTimingSentence({
        installCached: true,
        compileCached: true,
        unpackSeconds: 0.1,
        imageSeconds: 0.5,
        steps: 10,
        cachedSteps: 10,
      }),
    ).toBe("Build: install cached, compile cached, image export under 1s; 10 of 10 steps cached.")
  })

  test("without export lines the export step's own time stands in, and nothing else is made up", () => {
    expect(buildTimingSentence({ imageSeconds: 12, steps: 3, cachedSteps: 0 }, 40)).toBe(
      "Build 40s: image export 12s; 0 of 3 steps cached.",
    )
  })
})

describe("buildTimingOf", () => {
  test("reads only the Build step's record", () => {
    const timing = { steps: 2, cachedSteps: 1 }
    expect(buildTimingOf({ key: "build_artifact", evidence: { timing } })).toBe(timing)
    expect(buildTimingOf({ key: "render_runtime", evidence: { timing } })).toBeUndefined()
    expect(buildTimingOf({ key: "build_artifact", evidence: { timing: "x" } })).toBeUndefined()
    expect(buildTimingOf({ key: "build_artifact", evidence: {} })).toBeUndefined()
  })
})

describe("usualRunSeconds", () => {
  const run = (id, seconds, fields = {}) => ({
    id,
    runNumber: id,
    state: "succeeded",
    operation: "deploy",
    requestedAt: "2026-09-03T11:59:00Z",
    claimedAt: "2026-09-03T12:00:00Z",
    endedAt: new Date(Date.parse("2026-09-03T12:00:00Z") + seconds * 1000).toISOString(),
    ...fields,
  })

  test("is the median of the runs like this one that succeeded, from claim to end", () => {
    const recent = [run(9, 100), run(8, 125), run(7, 300), run(6, 10, { state: "failed" })]
    expect(usualRunSeconds(recent, { id: 10, operation: "deploy" })).toBe(125)
    expect(usualRunSeconds([...recent, run(5, 130)], { id: 10, operation: "deploy" })).toBe(127.5)
  })

  test("leaves out the run itself and other kinds, and says nothing on fewer than three", () => {
    const recent = [run(10, 50), run(9, 100), run(8, 125), run(7, 5, { operation: "restart" })]
    expect(usualRunSeconds(recent, { id: 10, operation: "deploy" })).toBeUndefined()
    // A redeploy builds as a deploy does.
    expect(
      usualRunSeconds([...recent, run(6, 110, { operation: "redeploy" })], {
        id: 10,
        operation: "deploy",
      }),
    ).toBe(110)
  })

  test("a run recorded before claims were listed is measured from its request", () => {
    const recent = [run(3, 60, { claimedAt: undefined }), run(2, 60), run(1, 60)]
    expect(usualRunSeconds(recent, { id: 4, operation: "deploy" })).toBe(60)
  })
})

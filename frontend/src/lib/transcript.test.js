import { describe, expect, test } from "bun:test"
import { classify, extendTranscript, transcriptLines, TRIMMED_MARKER } from "./transcript"

describe("a transcript's lines", () => {
  test("BuildKit's step, output, verdict and cache lines are told apart", () => {
    expect(classify("#23 [frontend deps 4/4] RUN bun install --frozen-lockfile")).toEqual({
      kind: "step",
      step: "23",
      target: "frontend deps 4/4",
      service: "frontend",
    })
    expect(classify("#23 0.412 installed next@16.3.5")).toEqual({
      kind: "output",
      step: "23",
      time: "0.412",
    })
    expect(classify("#23 DONE 40.0s")).toEqual({ kind: "done", step: "23", time: "40.0s" })
    expect(classify("#17 CACHED")).toEqual({ kind: "cached", step: "17" })
    expect(classify("#5 sha256:9f62 2.10MB / 14.14MB 0.3s").kind).toBe("progress")
  })

  test("an internal step names no service", () => {
    expect(classify("#1 [internal] load local bake definitions").service).toBeUndefined()
    expect(classify("#3 [backend internal] load build definition from Dockerfile").service).toBe(
      "backend",
    )
  })

  test("compose's resource lines carry the service and what happened to it", () => {
    expect(classify(" Container just-dashboard-backend-1 Healthy ")).toEqual({
      kind: "resource",
      resource: "Container",
      state: "Healthy",
      service: "backend",
    })
    expect(classify(" Image just-dashboard-frontend:latest Built ").service).toBe("frontend")
    expect(classify(" Container just-dashboard-proxy-1 Error").kind).toBe("error")
  })

  test("the runner's own sentences and commands are not output", () => {
    expect(classify("$ docker compose -f docker-compose.yml up -d").kind).toBe("command")
    expect(classify("Rebuilding Just Dashboard requested by wayy").kind).toBe("note")
    expect(
      classify("waiting for the dashboard to answer at http://127.0.0.1:42980/healthz").kind,
    ).toBe("note")
    expect(classify("The dashboard is running the new configuration.").kind).toBe("note")
  })

  test("a failure is loud and a count of none is not", () => {
    expect(classify("FAILED: docker compose build: exit status 1").kind).toBe("error")
    expect(classify("#31 ERROR: process did not complete successfully").kind).toBe("error")
    expect(classify("#29 12.1 error TS2322: Type 'string' is not assignable").kind).toBe("error")
    expect(classify("#29 30.2 Found 0 errors.").kind).toBe("output")
    expect(classify("#29 3.4 warning: deprecated option").kind).toBe("warning")
  })

  test("lines are numbered, escapes dropped and runs of blank lines folded", () => {
    const lines = transcriptLines("one\n\n\n\x1b[32mtwo\x1b[0m\r\n")
    expect(lines.map((line) => [line.number, line.text, line.kind])).toEqual([
      [1, "one", "output"],
      [2, "", "blank"],
      [3, "two", "output"],
    ])
    expect(transcriptLines("")).toEqual([])
  })

  test("the trimmed marker is its own kind of line", () => {
    expect(transcriptLines(`${TRIMMED_MARKER}\nlater`)[0].kind).toBe("trimmed")
  })
})

describe("extending the whole transcript by the polled tail", () => {
  const body = Array.from({ length: 400 }, (_, i) => `#${i % 7} line ${i}`).join("\n") + "\n"

  test("an untrimmed tail is the whole file", () => {
    expect(extendTranscript("stale", "short\n")).toEqual({ text: "short\n", overlapped: true })
  })

  test("what the tail has beyond the whole copy is appended once", () => {
    const grown = body + "#9 line 400\n#9 line 401\n"
    const tail = `${TRIMMED_MARKER}\n${grown.slice(grown.indexOf("#2 line 100"))}`
    const { text, overlapped } = extendTranscript(body, tail)
    expect(overlapped).toBe(true)
    expect(text).toBe(grown)
  })

  test("a tail that no longer overlaps says so rather than splicing", () => {
    const tail = `${TRIMMED_MARKER}\nsomething from another run entirely\n`
    expect(extendTranscript(body, tail)).toEqual({ text: tail, overlapped: false })
  })
})

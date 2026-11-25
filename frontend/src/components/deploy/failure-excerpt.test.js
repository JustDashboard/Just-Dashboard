import { describe, expect, test } from "bun:test"
import { failureExcerpt } from "./failure-excerpt"
import { consoleRows } from "./transcript-buffer"

/** One event per line, all from `stepId`, on `stream`. */
function events(lines, { stepId = 5, stream = "stderr", from = 1 } = {}) {
  return lines.map((text, index) => ({
    seq: from + index,
    stepId,
    ts: "2026-11-25T12:00:00Z",
    stream,
    text: `${text}\n`,
    truncated: false,
  }))
}

const texts = (excerpt) => excerpt.rows.map((row) => row.line.text)
const markedText = (excerpt) => excerpt.rows.find((row) => row.id === excerpt.marked)?.line.text

// A Docker build whose type check threw, as BuildKit prints it: the failing
// instruction's output, its ERROR, then the recap of its last lines and the
// Dockerfile around it, which is what the last lines of the step are.
const typecheckBuild = [
  "#13 [source 3/4] RUN bun install --frozen-lockfile",
  "#13 11.60 211 packages installed [10.79s]",
  "#13 DONE 23.0s",
  "",
  "#17 [typecheck 2/2] RUN --mount=type=cache,id=jd-typecheck,target=/tmp/jd-typecheck next typegen && node /opt/check.cjs",
  "#17 ...",
  "#17 1.905 Generating route types...",
  "#17 2.030 ✓ Types generated successfully",
  "#17 2.167 /opt/check.cjs:1",
  '#17 2.167 const ts = require("typescript"); if (!ok) throw new Error("no")',
  "#17 2.167                ^",
  "#17 2.167 ",
  "#17 2.167 TypeError: ts.getParsedCommandLineOfConfigFile is not a function",
  "#17 2.167     at Object.<anonymous> (/opt/check.cjs:1:128)",
  "#17 2.167     at Module._compile (node:internal/modules/cjs/loader:1781:14)",
  "#17 2.167     at Object..js (node:internal/modules/cjs/loader:1913:10)",
  "#17 2.167     at Module.load (node:internal/modules/cjs/loader:1505:32)",
  "#17 2.167 ",
  "#17 2.167 Node.js v22.23.5",
  '#17 ERROR: process "/bin/sh -c next typegen && node /opt/check.cjs" did not complete successfully: exit code: 1',
  "#18 [build 2/2] RUN next build",
  "#18 CANCELED",
  "------",
  " > [typecheck 2/2] RUN next typegen && node /opt/check.cjs:",
  "2.167     at Module.load (node:internal/modules/cjs/loader:1505:32)",
  "2.167 Node.js v22.23.5",
  "------",
  "Dockerfile:14",
  "--------------------",
  "  13 |     ARG JD_BUILD_VALUES_1",
  "  14 | >>> RUN --mount=type=cache,id=jd-typecheck,target=/tmp/jd-typecheck next typegen",
  "--------------------",
  'ERROR: failed to build: failed to solve: process "/bin/sh -c next typegen" did not complete successfully: exit code: 1',
]

describe("failureExcerpt", () => {
  test("a failed Docker build shows the error its instruction printed, not the recap after it", () => {
    const rows = consoleRows([
      ...events(typecheckBuild),
      ...events(["Diagnosis: The image setup step exited with code 1."], {
        stream: "status",
        from: 100,
      }),
    ])
    const excerpt = failureExcerpt(rows, 5)
    expect(markedText(excerpt)).toBe(
      "#17 2.167 TypeError: ts.getParsedCommandLineOfConfigFile is not a function",
    )
    // The thrown error and the frame that threw it; the runtime's own frames
    // past the second are left to the console.
    expect(texts(excerpt)).toEqual([
      "#17 2.167 TypeError: ts.getParsedCommandLineOfConfigFile is not a function",
      "#17 2.167     at Object.<anonymous> (/opt/check.cjs:1:128)",
      "#17 2.167     at Module._compile (node:internal/modules/cjs/loader:1781:14)",
      "#17 2.167 Node.js v22.23.5",
    ])
    // A line that only mentions an error in passing does not open one.
    expect(texts(excerpt)).not.toContain(
      '#17 2.167 const ts = require("typescript"); if (!ok) throw new Error("no")',
    )
  })

  test("a framework's failure headline is read from its top, with the lines that say where", () => {
    const rows = consoleRows(
      events([
        "#20 [build 2/2] RUN bun run build",
        "#20 0.75 $ next build",
        "#20 2.10 ▲ Next.js 16.4.6",
        "#20 41.2 Failed to compile.",
        "#20 41.2 ",
        "#20 41.2 ./src/app/page.tsx:12:5",
        "#20 41.2 Type error: Type 'string' is not assignable to type 'number'.",
        '#20 41.3 error: script "build" exited with code 1',
        '#20 ERROR: process "/bin/sh -c bun run build" did not complete successfully: exit code: 1',
        "ERROR: failed to build: failed to solve: exit code: 1",
      ]),
    )
    const excerpt = failureExcerpt(rows, 5, undefined, 4)
    expect(texts(excerpt)).toEqual([
      "#20 41.2 Failed to compile.",
      "#20 41.2 ./src/app/page.tsx:12:5",
      "#20 41.2 Type error: Type 'string' is not assignable to type 'number'.",
      '#20 41.3 error: script "build" exited with code 1',
    ])
  })

  test("a TypeScript diagnostic opens the excerpt", () => {
    const rows = consoleRows(
      events([
        "#9 [typecheck 2/2] RUN node /opt/check.cjs",
        "#9 3.10 src/app/page.tsx(12,5): error TS2322: Type 'string' is not assignable to type 'number'.",
        '#9 ERROR: process "/bin/sh -c node /opt/check.cjs" did not complete successfully: exit code: 1',
      ]),
    )
    expect(markedText(failureExcerpt(rows, 5))).toContain("error TS2322")
  })

  test("the line the diagnosis proved is shown with the one before it", () => {
    const rows = consoleRows(
      events(["one", "two", "npm ERR! code ERESOLVE", "four", "five", "six", "seven", "eight"]),
    )
    const excerpt = failureExcerpt(rows, 5, 4, 3)
    expect(markedText(excerpt)).toBe("four")
    expect(texts(excerpt)).toEqual(["npm ERR! code ERESOLVE", "four", "five"])
  })

  test("a failing instruction that printed no error is its last lines", () => {
    const rows = consoleRows(
      events([
        "#4 [build 1/1] RUN ./build.sh",
        "#4 0.10 compiling",
        "#4 0.20 linking",
        '#4 ERROR: process "/bin/sh -c ./build.sh" did not complete successfully: exit code: 2',
      ]),
    )
    const excerpt = failureExcerpt(rows, 5)
    expect(excerpt.marked).toBeUndefined()
    expect(texts(excerpt)).toEqual(["#4 0.10 compiling", "#4 0.20 linking"])
  })

  test("output outside BuildKit is read from its last error, without the engine's own sentences", () => {
    const rows = consoleRows([
      ...events(["Application output from jd-e1-r2"], { stream: "status" }),
      ...events(
        [
          "listening",
          "Error: Cannot find module '/app/server.js'",
          "    at Module._resolveFilename (node:internal/modules/cjs/loader:1225:15)",
          "Node.js v22.23.5",
        ],
        { from: 10 },
      ),
      ...events(["Diagnosis: The start command exited."], { stream: "status", from: 20 }),
    ])
    const excerpt = failureExcerpt(rows, 5)
    expect(markedText(excerpt)).toBe("Error: Cannot find module '/app/server.js'")
    expect(texts(excerpt)).not.toContain("Diagnosis: The start command exited.")
  })

  test("a step whose only lines are the engine's still shows them", () => {
    const rows = consoleRows(
      events(
        [
          "Resolving a certificate for shop.example.test",
          "nginx could not serve the certificate challenge",
        ],
        { stream: "status" },
      ),
    )
    expect(texts(failureExcerpt(rows, 5))).toEqual([
      "Resolving a certificate for shop.example.test",
      "nginx could not serve the certificate challenge",
    ])
  })

  test("another step's lines and blank lines are never part of it", () => {
    const rows = consoleRows([
      ...events(["fatal: from another step"], { stepId: 4 }),
      ...events(["", "done"], { from: 10 }),
    ])
    expect(texts(failureExcerpt(rows, 5))).toEqual(["done"])
    expect(failureExcerpt(rows, 9).rows).toEqual([])
  })
})

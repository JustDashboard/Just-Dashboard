import { expect, test } from "bun:test"
import { compare, LIMIT, measure, measureSource, readRecord } from "./function-sizes.ts"

const line = ({ file, name, was, now }) =>
  was === undefined ? `${file}: ${name} is ${now} lines` : `${file}: ${name} ${was} → ${now} lines`

test(`no function in src crosses ${LIMIT} lines, and none recorded over it grows`, () => {
  const { crossed, grown, shrunk, gone } = compare(readRecord(), measure())
  if (shrunk.length + gone.length > 0) {
    // Shorter passes; the record comes down so the next change is held to the new length.
    console.log(
      [
        `Shorter than function-sizes.json says (${shrunk.length + gone.length}); run`,
        "`bun scripts/function-sizes.ts --write` in frontend/ and commit the record:",
        ...shrunk.map(line),
        ...gone.map(({ file, name, was }) => `${file}: ${name} ${was} → at most ${LIMIT}, or gone`),
      ].join("\n  "),
    )
  }
  // Split the function rather than raise the record: a body this long is changed all at once
  // by every change to any part of it (CQ-03). The record goes up only for a rename or a move,
  // which the pull request says.
  expect(crossed.map(line)).toEqual([])
  expect(grown.map(line)).toEqual([])
  // Parsing the long files is a second or two, and several times that on a loaded machine.
}, 60_000)

/** A source whose one function body is `lines` long, after a doc comment that does not count. */
const body = (head, lines, tail = "}") =>
  `/** said\n * about it\n */\n${head}\n${"  x()\n".repeat(lines - 2)}${tail}\n`

test("a function's length runs from its first line to its last, the comment above left out", () => {
  expect(measureSource("a.ts", body("function big() {", 6), 5)).toEqual({ big: 6 })
  expect(measureSource("a.ts", body("function exact() {", 5), 5)).toEqual({})
})

test("a function is named by what holds it", () => {
  const sizes = (head, tail = "}") => measureSource("a.tsx", body(head, 6, tail), 5)
  expect(sizes("export default function Page() {")).toEqual({ Page: 6 })
  expect(sizes("const Row = () => {")).toEqual({ Row: 6 })
  expect(sizes("const Row = memo(function () {", "})")).toEqual({ Row: 6 })
  expect(sizes("const onKey = useCallback(() => {", "}, [])")).toEqual({ onKey: 6 })
  expect(sizes("useEffect(() => {", "}, [])")).toEqual({ "useEffect(…)": 6 })
  expect(sizes("const box = { draw() {", "} }")).toEqual({ draw: 6 })
  expect(sizes("class Board { paint() {", "} }")).toEqual({ "Board › paint": 6 })
  expect(sizes("const a = <b onClick={() => {", "}} />")).toEqual({ "onClick=…": 6 })
})

test("a long function inside a long one is its own entry; two of one name are told apart", () => {
  const inner = (name) => `  ${name}(() => {\n${"    x()\n".repeat(5)}  })\n`
  const source = `function Page() {\n${inner("useEffect")}${inner("useEffect")}}\n`
  expect(measureSource("a.tsx", source, 5)).toEqual({
    Page: 16,
    "Page › useEffect(…)": 7,
    "Page › useEffect(…) #2": 7,
  })
})

test("a file is passed over only when it is too short to hold a function over the limit", () => {
  expect(measureSource("a.ts", "function a() {\n\n\n\n\n}", 5)).toEqual({ a: 6 })
  expect(measureSource("a.ts", "function a() {\n\n\n\n}", 5)).toEqual({})
})

test("crossing the limit and growing fail; shrinking and leaving pass and are reported", () => {
  const record = { "src/a.tsx": { Page: 900, Form: 600 }, "src/b.tsx": { Old: 700 } }
  const now = { "src/a.tsx": { Page: 901, Form: 550, Fresh: 501 }, "src/c.tsx": { Moved: 700 } }
  expect(compare(record, now)).toEqual({
    crossed: [
      { file: "src/a.tsx", name: "Fresh", now: 501 },
      { file: "src/c.tsx", name: "Moved", now: 700 },
    ],
    grown: [{ file: "src/a.tsx", name: "Page", was: 900, now: 901 }],
    shrunk: [{ file: "src/a.tsx", name: "Form", was: 600, now: 550 }],
    gone: [{ file: "src/b.tsx", name: "Old", was: 700 }],
  })
  expect(compare(record, record)).toEqual({ crossed: [], grown: [], shrunk: [], gone: [] })
})

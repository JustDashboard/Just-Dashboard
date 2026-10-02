import { describe, expect, test } from "bun:test"
import {
  STAGES,
  STAGE_KINDS,
  enabledStages,
  exportText,
  listText,
  moved,
  newStage,
  parsePipeline,
  pipelineKind,
  pipelineText,
  previewIndex,
  sameAsSaved,
  shellCollection,
  stageProblem,
  stageKind,
  stageSpec,
  withOperator,
  withSaved,
  withoutSaved,
} from "./pipeline"

const stage = (id, op, body, enabled = true) => ({ id, op, body, enabled })

describe("the pipeline that is sent", () => {
  const stages = [
    stage("a", "$match", '{ status: "paid" }'),
    stage("b", "$sort", "{ total: -1 }", false),
    stage("c", "$limit", "5"),
  ]

  test("the enabled stages, each body exactly as typed", () => {
    expect(pipelineText(stages)).toBe('[{ "$match": { status: "paid" } }, { "$limit": 5 }]')
    expect(pipelineText([])).toBe("[]")
    expect(enabledStages(stages).map((entry) => entry.id)).toEqual(["a", "c"])
  })

  test("a preview asks for a stage by its place among the enabled ones", () => {
    expect(previewIndex(stages, "a")).toBe(0)
    expect(previewIndex(stages, "c")).toBe(1)
    expect(previewIndex(stages, "b")).toBe(-1)
  })
})

describe("whether a run would write", () => {
  test("read off the stage operators, never off the text", () => {
    // M-extra: the old page looked for "$out" or "$merge" anywhere in the
    // text, so a $mergeObjects in a $group made a read ask for confirmation.
    const reads = [
      stage("a", "$group", '{ _id: "$k", all: { $mergeObjects: "$doc" }, note: "$out" }'),
      stage("b", "$lookup", '{ from: "x", pipeline: [], as: "j" }'),
    ]
    expect(pipelineKind(reads)).toEqual({ writes: null, unknown: [] })
  })

  test("a pipeline that ends in $out or $merge writes", () => {
    expect(pipelineKind([stage("a", "$match", "{}"), stage("b", "$out", '"copy"')]).writes).toBe(
      "$out",
    )
    expect(pipelineKind([stage("a", "$merge", '{ into: "x" }')]).writes).toBe("$merge")
  })

  test("a writing stage that is switched off does not make the pipeline write", () => {
    expect(pipelineKind([stage("a", "$out", '"copy"', false)])).toEqual({
      writes: null,
      unknown: [],
    })
  })

  test("a stage the server does not know to be a read is named, as it treats the run as writing", () => {
    expect(
      pipelineKind([stage("a", "$changeStream", "{}"), stage("b", "$changeStream", "{}")]),
    ).toEqual({ writes: null, unknown: ["$changeStream"] })
  })

  test("every stage the builder offers is one the server knows, or one that writes", () => {
    for (const spec of STAGES) {
      const kind = pipelineKind([stage("a", spec.op, spec.snippet)])
      expect(kind.unknown).toEqual([])
      expect(kind.writes).toBe(spec.op === "$out" || spec.op === "$merge" ? spec.op : null)
    }
  })
})

describe("what a stage does", () => {
  test("every stage the builder offers has a kind the picker lists", () => {
    const listed = new Set(STAGE_KINDS.map((entry) => entry.kind))
    for (const spec of STAGES) expect(listed.has(spec.kind)).toBe(true)
    // And every kind has a stage: the picker draws no empty group.
    for (const { kind } of STAGE_KINDS) {
      expect(STAGES.some((spec) => spec.kind === kind)).toBe(true)
    }
  })

  test("the stages that write are the only ones of the writing kind", () => {
    expect(STAGES.filter((spec) => spec.kind === "write").map((spec) => spec.op)).toEqual([
      "$out",
      "$merge",
    ])
  })

  test("a stage by its operator; one the builder does not know has no kind", () => {
    expect(stageKind("$match")).toBe("filter")
    expect(stageKind("$group")).toBe("group")
    expect(stageKind("$lookup")).toBe("join")
    expect(stageKind("$project")).toBe("shape")
    expect(stageKind("$changeStream")).toBeUndefined()
  })
})

describe("a stage as typed", () => {
  test("a body is one whole value", () => {
    expect(stageProblem(stage("a", "$match", "{ a: 1 }"))).toBeNull()
    expect(stageProblem(stage("a", "$limit", "5"))).toBeNull()
    expect(stageProblem(stage("a", "$count", '"total"'))).toBeNull()
    expect(stageProblem(stage("a", "$match", "{ a: "))).toBe(
      "Line 1, column 6: the text ends where a value was expected.",
    )
    expect(stageProblem(stage("a", "$match", ""))).toBe("Write what $match is given.")
    expect(stageProblem(stage("a", "match", "{}"))).toBe("A stage operator begins with $.")
  })

  test("a new stage starts as its operator's own snippet, which reads", () => {
    for (const spec of STAGES) {
      const made = newStage("x", spec.op)
      expect(made.body).toBe(spec.snippet)
      // The snippet for $match is a document still to be filled in.
      expect(stageProblem(made)).toBeNull()
    }
    expect(stageSpec("$nope")).toBeUndefined()
  })

  test("another operator brings its snippet only while the body is untouched", () => {
    const fresh = newStage("x", "$match")
    expect(withOperator(fresh, "$limit").body).toBe("10")
    const typed = { ...fresh, body: "{ a: 1 }" }
    expect(withOperator(typed, "$limit")).toMatchObject({ op: "$limit", body: "{ a: 1 }" })
  })
})

describe("the pipeline as text", () => {
  const stages = [
    stage("a", "$match", '{\n  status: "paid"\n}'),
    stage("b", "$sort", "{ total: -1 }", false),
  ]

  test("for a shell: a stage that is off is kept as a comment", () => {
    expect(exportText("orders", stages)).toBe(
      'db.orders.aggregate([\n  { $match: {\n    status: "paid"\n  } },\n  // { $sort: { total: -1 } }\n])',
    )
    expect(shellCollection("my-coll")).toBe('db.getCollection("my-coll")')
  })

  test("as a list to edit: the enabled stages", () => {
    expect(listText(stages)).toBe('[\n  { $match: {\n    status: "paid"\n  } }\n]')
    expect(listText([])).toBe("[\n  \n]")
  })

  test("text read back is the stages it spells", () => {
    const parsed = parsePipeline(
      '[\n  { $match: { status: "paid" } },\n  { "$group": {\n    _id: "$userId",\n    n: { $sum: 1 }\n  } },\n  { $limit: 5 },\n]',
    )
    expect(parsed).toEqual({
      ok: true,
      stages: [
        { op: "$match", body: '{ status: "paid" }' },
        { op: "$group", body: '{\n  _id: "$userId",\n  n: { $sum: 1 }\n}' },
        { op: "$limit", body: "5" },
      ],
    })
  })

  test("what the builder writes, it reads back", () => {
    const enabled = [
      stage("a", "$match", '{\n  status: "paid"\n}'),
      stage("b", "$count", '"total"'),
    ]
    const parsed = parsePipeline(listText(enabled))
    expect(parsed.ok && parsed.stages).toEqual(
      enabled.map((entry) => ({ op: entry.op, body: entry.body })),
    )
  })

  test("a whole aggregate call pasted from elsewhere", () => {
    const parsed = parsePipeline('db.orders.aggregate([ { $match: { a: 1 } }, { $count: "n" } ]);')
    expect(parsed.ok && parsed.stages.map((entry) => entry.op)).toEqual(["$match", "$count"])
  })

  test("text that is not a pipeline says why", () => {
    expect(parsePipeline("{ $match: {} }")).toEqual({
      ok: false,
      message: "A pipeline is a list: [ … ].",
    })
    expect(parsePipeline("[ { $match: {}, $sort: {} } ]").message).toContain("Stage 1")
    expect(parsePipeline("[ { match: {} } ]").message).toContain("match is not a stage operator")
    expect(parsePipeline("[ { $match: { ").message).toContain("Line 1")
    expect(parsePipeline("")).toEqual({ ok: true, stages: [] })
  })
})

describe("reordering and saving", () => {
  const stages = [stage("a", "$match", "{}"), stage("b", "$sort", "{}"), stage("c", "$limit", "1")]

  test("a stage moves one step, and stays put at an end", () => {
    expect(moved(stages, "b", -1).map((entry) => entry.id)).toEqual(["b", "a", "c"])
    expect(moved(stages, "b", 1).map((entry) => entry.id)).toEqual(["a", "c", "b"])
    expect(moved(stages, "a", -1).map((entry) => entry.id)).toEqual(["a", "b", "c"])
    expect(moved(stages, "c", 1).map((entry) => entry.id)).toEqual(["a", "b", "c"])
  })

  test("a pipeline saved under a name replaces the one of that name, newest first", () => {
    const first = { name: "one", stages: [], savedAt: 1 }
    const second = { name: "two", stages: [], savedAt: 2 }
    const again = { name: "one", stages: [{ op: "$limit", body: "1", enabled: true }], savedAt: 3 }
    const list = withSaved(withSaved(withSaved([], first), second), again)
    expect(list.map((entry) => [entry.name, entry.savedAt])).toEqual([
      ["one", 3],
      ["two", 2],
    ])
    expect(withoutSaved(list, "one").map((entry) => entry.name)).toEqual(["two"])
  })

  test("the builder's stages against the saved ones", () => {
    const saved = {
      name: "s",
      savedAt: 1,
      stages: stages.map(({ op, body, enabled }) => ({ op, body, enabled })),
    }
    expect(sameAsSaved(stages, saved)).toBe(true)
    expect(sameAsSaved(stages.slice(0, 2), saved)).toBe(false)
    expect(sameAsSaved([{ ...stages[0], enabled: false }, ...stages.slice(1)], saved)).toBe(false)
    expect(sameAsSaved(stages, undefined)).toBe(false)
  })
})

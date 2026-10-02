import { describe, expect, test } from "bun:test"
import {
  countsLine,
  editorLanguage,
  firstProblem,
  groupTargets,
  readResult,
  readTargets,
  requestBody,
  resolveOptions,
  tableName,
  unsupportedReason,
} from "./options"

const prisma = {
  id: "prisma",
  label: "Prisma",
  filename: "schema.prisma",
  description: "A schema.prisma to drop into an existing Prisma project.",
  language: "prisma",
  group: "ORM",
  engines: ["postgres", "mysql", "sqlite", "sqlserver"],
  unsupported: { clickhouse: "Prisma has no connector for ClickHouse." },
  options: [
    { id: "relations", label: "Relations", description: "", type: "boolean", default: true },
    { id: "views", label: "Views", description: "", type: "boolean", default: false },
    {
      id: "naming",
      label: "Naming",
      description: "",
      type: "select",
      default: "preserve",
      choices: [
        { value: "preserve", label: "As in the database" },
        { value: "camel", label: "camelCase" },
      ],
    },
  ],
}
const gorm = {
  ...prisma,
  id: "gorm",
  label: "GORM",
  language: "go",
  engines: ["postgres", "clickhouse"],
  unsupported: {},
  options: [{ id: "package", label: "Package", description: "", type: "text", default: "models" }],
}
const sql = { ...prisma, id: "sql", label: "SQL", group: "Schema", options: [] }
const zod = { ...prisma, id: "zod", label: "Zod", group: "Types & validation", options: [] }

describe("the catalogue of targets", () => {
  test("is read whatever came back", () => {
    expect(readTargets([])).toEqual([])
    expect(readTargets(null)).toEqual([])
    expect(readTargets({ targets: [prisma, { nope: true }] })).toHaveLength(1)
  })
  test("a target that says little is still a target", () => {
    const [bare] = readTargets({ targets: [{ id: "new" }] })
    expect(bare).toEqual({
      id: "new",
      label: "new",
      filename: "",
      description: "",
      language: "",
      group: "Other",
      engines: [],
      unsupported: {},
      options: [],
    })
  })
  test("is listed by group, in the page's order, a group the server invents last", () => {
    const groups = groupTargets([sql, { ...zod, group: "Diagrams" }, prisma, zod, gorm])
    expect(groups.map((entry) => [entry.group, entry.targets.map((target) => target.id)])).toEqual([
      ["ORM", ["prisma", "gorm"]],
      ["Types & validation", ["zod"]],
      ["Schema", ["sql"]],
      ["Diagrams", ["zod"]],
    ])
  })
  test("a target an engine has no connector for says why, in the server's words", () => {
    expect(unsupportedReason(prisma, "postgres")).toBeUndefined()
    expect(unsupportedReason(prisma, "clickhouse")).toBe("Prisma has no connector for ClickHouse.")
    expect(unsupportedReason(prisma, "oracle")).toBe("Prisma is not generated for this engine.")
    expect(unsupportedReason(prisma, "constructor")).toBe(
      "Prisma is not generated for this engine.",
    )
  })
})

describe("a target's switches", () => {
  test("start at the server's defaults", () => {
    expect(resolveOptions(prisma, undefined)).toEqual({
      relations: true,
      views: false,
      naming: "preserve",
    })
  })
  test("keep what the reader chose, where the switch still takes it", () => {
    expect(
      resolveOptions(prisma, { relations: false, naming: "camel", views: "yes", gone: true }),
    ).toEqual({
      relations: false,
      views: false,
      naming: "camel",
    })
    expect(resolveOptions(prisma, { naming: "snake" }).naming).toBe("preserve")
  })
  test("only the listed switches are sent, and only where they differ from the default", () => {
    expect(
      requestBody(
        prisma,
        { relations: false, views: false, naming: "camel", split: true },
        { schemas: ["public"], tables: [] },
      ),
    ).toEqual({ target: "prisma", schema: "public", relations: false, naming: "camel" })
  })
  test("one schema is `schema`, several are `schemas`, chosen tables are `tables`", () => {
    expect(requestBody(sql, {}, { schemas: [], tables: [] })).toEqual({ target: "sql" })
    expect(requestBody(sql, {}, { schemas: ["a", "b"], tables: ["a.t"] })).toEqual({
      target: "sql",
      schemas: ["a", "b"],
      tables: ["a.t"],
    })
  })
  test("a Go package name the server would refuse is said, and not sent", () => {
    expect(firstProblem(gorm, { package: "models" })).toBeUndefined()
    expect(firstProblem(gorm, { package: "My-Models" })).toContain(
      "Package: A lower-case Go package name",
    )
    expect(requestBody(gorm, { package: "My-Models" }, { schemas: [], tables: [] })).toEqual({
      target: "gorm",
    })
    expect(requestBody(gorm, { package: "store" }, { schemas: [], tables: [] })).toEqual({
      target: "gorm",
      package: "store",
    })
  })
})

describe("what was generated", () => {
  test("is read as files whatever came back, the old one-file answer included", () => {
    expect(
      readResult({ target: "prisma", schema: "model A {}", filename: "schema.prisma" }).files,
    ).toEqual([{ filename: "schema.prisma", content: "model A {}" }])
    const full = readResult({
      target: "diesel",
      language: "rust",
      files: [
        { filename: "schema.rs", content: "a" },
        { filename: "models.rs", content: "b" },
      ],
      warnings: ["w"],
      counts: { tables: 3, views: 0, enums: 0, relations: 2 },
    })
    expect(full.files.map((file) => file.filename)).toEqual(["schema.rs", "models.rs"])
    expect(full.warnings).toEqual(["w"])
    expect(readResult([]).files).toEqual([])
    expect(readResult(null).warnings).toEqual([])
  })
  test("is counted in a line, the zeroes left out", () => {
    expect(countsLine({ tables: 7, views: 1, enums: 2, relations: 6 })).toBe(
      "7 tables · 1 view · 2 enums · 6 relations",
    )
    expect(countsLine({ tables: 1, views: 0, enums: 0, relations: 0 })).toBe("1 table")
  })
  test("is highlighted by its language, a Prisma schema as plain text", () => {
    expect(editorLanguage("typescript")).toBe("typescript")
    expect(editorLanguage("prisma")).toBe("plaintext")
    expect(editorLanguage("")).toBe("plaintext")
  })
  test("a table is named bare while one schema is read, with its schema once several are", () => {
    expect(tableName("public", "orders", false)).toBe("orders")
    expect(tableName("public", "orders", true)).toBe("public.orders")
    expect(tableName("", "orders", true)).toBe("orders")
  })
})

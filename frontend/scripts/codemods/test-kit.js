/**
 * What every codemod test shares: reading a fixture pair, and running run.ts on a copy of the
 * old spelling in a scratch tree, so `--check`, `--dry-run` and a write are tested as a person
 * runs them.
 */
import { expect } from "bun:test"
import { spawnSync } from "node:child_process"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const HERE = dirname(fileURLToPath(import.meta.url))

/** A fixture is `.tsx.fixture` so neither tsc nor ESLint reads its deliberately old spellings. */
export function fixture(name, which) {
  return readFileSync(join(HERE, "fixtures", name, `${which}.tsx.fixture`), "utf8")
}

export function cli(args, cwd) {
  const out = spawnSync(process.execPath, [join(HERE, "run.ts"), ...args], {
    cwd,
    encoding: "utf8",
  })
  return { status: out.status, stdout: out.stdout, stderr: out.stderr }
}

/**
 * Applies the transform to its fixture, checks the result, and that a second pass finds nothing
 * left to rewrite. Returns the findings, for the test to assert what was left for review.
 */
export function expectFixture(transform, options = {}, file = "src/components/demo.tsx") {
  const before = fixture(transform.name, "before")
  const after = fixture(transform.name, "after")
  const once = transform.apply(before, file, options)
  expect(once.text).toBe(after)
  const twice = transform.apply(once.text, file, options)
  expect(twice.text).toBe(after)
  expect(twice.findings.filter((f) => f.kind === "rewrite")).toEqual([])
  return once.findings
}

/** Lines of the findings as `line kind from → to`, which reads well in a toEqual. */
export function lines(findings, kind) {
  return findings
    .filter((f) => !kind || f.kind === kind)
    .map((f) => `${f.line} ${f.kind} ${f.from}${f.to !== undefined ? ` → ${f.to}` : ""}`)
}

/**
 * The command line on a scratch copy: --check fails while the old spelling is there, --dry-run
 * changes nothing, a write produces the fixture's after, and --check then passes.
 */
export function expectCommandLine(name, extra = []) {
  const top = mkdtempSync(join(tmpdir(), `codemod-${name}-`))
  try {
    const path = join(top, "src/components/demo.tsx")
    mkdirSync(dirname(path), { recursive: true })
    const before = fixture(name, "before")
    writeFileSync(path, before)
    const flags = ["--no-guard", "--no-format", "--src", join(top, "src"), ...extra]

    const check = cli([name, "src", "--check", ...flags], top)
    expect(check.status).toBe(1)
    expect(check.stdout).toContain(`rewrite  ${name}`)

    const dry = cli([name, "src", "--dry-run", ...flags], top)
    expect(dry.status).toBe(0)
    expect(dry.stdout).toContain("--- src/components/demo.tsx")
    expect(readFileSync(path, "utf8")).toBe(before)

    const write = cli([name, "src", ...flags], top)
    expect(write.status).toBe(0)
    expect(readFileSync(path, "utf8")).toBe(fixture(name, "after"))

    const again = cli([name, "src", "--check", ...flags], top)
    expect(again.status).toBe(0)
  } finally {
    rmSync(top, { recursive: true, force: true })
  }
}

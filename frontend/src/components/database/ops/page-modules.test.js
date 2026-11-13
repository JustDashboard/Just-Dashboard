import { expect, test } from "bun:test"
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs"
import { dirname, join, relative, resolve } from "node:path"

// A route's entry JavaScript is everything its page file reaches by a static
// import, and the bundler keeps a module whole: it does not drop the exports a
// route never names, nor what those exports import. So a file that re-exports
// thirteen pages hands every one of them to each route that names one. That
// was `ops/index.tsx` until 0.8.6, with `ops/settings.tsx` behind it holding
// the eight settings pages as one module: the Version page shipped Access,
// Alerts, Backups and recharts, 1.2 MB it never ran. `mongo/index.tsx` and
// `redis/index.tsx` did the same to Data, Query, Schema and Performance.
//
// This walks the imports the way the bundler does and holds the rule: a
// database route imports its page from a module that is that page's alone,
// and reaches the page of no other route.

const SRC = resolve(import.meta.dir, "../../..")
const ROUTES = join(SRC, "app/(dashboard)/databases/[id]")
const PAGES = "components/database/"
// What every route stands in, not a page: the shell that picks the engine's page.
const SHELL = "components/database/shell/"

/** Where a specifier leads inside `src`, or null for a package. */
function resolveImport(from, specifier) {
  const base = specifier.startsWith("@/")
    ? join(SRC, specifier.slice(2))
    : specifier.startsWith(".")
      ? resolve(dirname(from), specifier)
      : null
  if (!base) return null
  for (const tail of ["", ".ts", ".tsx", "/index.ts", "/index.tsx"]) {
    const path = base + tail
    if (existsSync(path) && statSync(path).isFile()) return path
  }
  return null
}

/** The modules a file brings in when it loads: static imports and re-exports that are not types. */
function imports(file) {
  const text = readFileSync(file, "utf8")
  const out = []
  const statement = /^(?:import|export)\s+(?!type\b)(?:[^"';]*?\sfrom\s+)?["']([^"']+)["']/gm
  for (const match of text.matchAll(statement)) {
    const to = resolveImport(file, match[1])
    if (to) out.push(to)
  }
  return out
}

function reached(entry) {
  const seen = new Set([entry])
  const queue = [entry]
  while (queue.length) {
    for (const next of imports(queue.pop())) {
      if (seen.has(next)) continue
      seen.add(next)
      queue.push(next)
    }
  }
  return seen
}

function pageFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) =>
    entry.isDirectory()
      ? pageFiles(join(dir, entry.name))
      : entry.name === "page.tsx"
        ? [join(dir, entry.name)]
        : [],
  )
}

const name = (file) => relative(SRC, file)

// Every database route, with the modules it imports its page (or its three
// pages, one an engine) from.
const routes = pageFiles(ROUTES).map((route) => ({
  route,
  pages: imports(route).filter(
    (file) => name(file).startsWith(PAGES) && !name(file).startsWith(SHELL),
  ),
}))
const pageModules = new Set(routes.flatMap((one) => one.pages))

test("every database route is read, and each imports a page", () => {
  expect(routes.length).toBeGreaterThanOrEqual(22)
  for (const one of routes) {
    // The route for an address no page has draws the shell's own "not found".
    if (name(one.route).includes("[...missing]")) continue
    expect(one.pages.length, name(one.route)).toBeGreaterThan(0)
  }
})

test("a page's module is one route's: no index or shared file hands several pages out", () => {
  const owners = new Map()
  for (const one of routes) {
    for (const page of one.pages) owners.set(page, [...(owners.get(page) ?? []), name(one.route)])
  }
  const shared = [...owners]
    .filter(([, by]) => by.length > 1)
    .map(([page, by]) => `${name(page)} is imported by ${by.length} routes`)
  expect(shared).toEqual([])
  for (const folder of ["ops", "mongo", "redis"]) {
    expect(existsSync(join(SRC, PAGES, folder, "index.tsx")), `${folder}/index.tsx`).toBe(false)
    expect(existsSync(join(SRC, PAGES, folder, "index.ts")), `${folder}/index.ts`).toBe(false)
  }
})

// One page is meant to be reached from another: the SQL editor's assistant is
// the Assistant page's own scope and notice, which sit beside that page in a
// file of a hundred lines. A second entry here is a page shipping another.
const MEANT = new Set([
  "app/(dashboard)/databases/[id]/query/page.tsx reaches components/database/assistant.tsx",
])

test("a database route does not reach the page of another", () => {
  const strays = []
  for (const one of routes) {
    const own = new Set(one.pages)
    for (const file of reached(one.route)) {
      if (pageModules.has(file) && !own.has(file)) {
        strays.push(`${name(one.route)} reaches ${name(file)}`)
      }
    }
  }
  expect(strays.filter((one) => !MEANT.has(one))).toEqual([])
})

test("the walk follows a re-export, so a new index would be seen", () => {
  // `kit/index.ts` is a list of re-exports; a walk that missed them would pass anything.
  const kit = join(SRC, "components/database/kit/index.ts")
  expect(imports(kit).length).toBeGreaterThan(3)
})

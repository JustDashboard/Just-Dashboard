import { expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"

const frontend = join(import.meta.dir, "..", "..")

// The lockfile format moves with Bun's releases, so a floating tag lets an image
// built later install with a Bun nobody has tried the lockfile against.
test("the image installs with the Bun package.json pins", () => {
  const manifest = JSON.parse(readFileSync(join(frontend, "package.json"), "utf8"))
  const dockerfile = readFileSync(join(frontend, "Dockerfile"), "utf8")
  const version = manifest.packageManager.replace(/^bun@/, "")
  expect(dockerfile).toContain(`FROM oven/bun:${version} AS deps`)
})

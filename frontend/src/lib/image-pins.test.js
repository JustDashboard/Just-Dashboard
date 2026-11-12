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
  // With the digest scripts/pin-images.py holds every image of the stack to:
  // the tag says which Bun, the digest which build of it.
  const escaped = version.replaceAll(".", "\\.")
  expect(dockerfile).toMatch(
    new RegExp(`^FROM oven/bun:${escaped}@sha256:[0-9a-f]{64} AS deps$`, "m"),
  )
})

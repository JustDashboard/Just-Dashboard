import { expect, test } from "bun:test"
import { PACKAGE_LOGS, packageLogSources, packageLogsOutsideRoots } from "./package-logs"

const found = (path, label) => ({
  path,
  source: { id: `file:${path}`, label, kind: "system", path, lens: "packages", rotated: false },
})
const missing = (path) => ({ path, refused: "missing" })

test("apt's transactions come first, then dpkg, then what ran unattended", () => {
  const probes = PACKAGE_LOGS.map((path) =>
    path.includes("dnf") || path.endsWith("term.log")
      ? missing(path)
      : found(path, path.slice(path.lastIndexOf("/") + 1)),
  )
  expect(packageLogSources(probes).map((s) => s.label)).toEqual([
    "history.log",
    "dpkg.log",
    "unattended-upgrades.log",
    "unattended-upgrades-dpkg.log",
  ])
})

test("each is drawn as the distribution, and is the server's description, not asked again", () => {
  const [source] = packageLogSources([found("/var/log/dnf.log", "dnf")], "fedora")
  expect(source.id).toBe("file:/var/log/dnf.log")
  expect(source.product).toBe("fedora")
  // The lens the server detected, as it described the file.
  expect(source.lens).toBe("packages")
  expect(source.described).toBe(true)
})

test("a host with no package log offers none, and says when the roots are why", () => {
  const none = PACKAGE_LOGS.map(missing)
  expect(packageLogSources(none)).toEqual([])
  expect(packageLogsOutsideRoots(none)).toBe(false)
  const refused = PACKAGE_LOGS.map((path) => ({ path, refused: "outside" }))
  expect(packageLogsOutsideRoots(refused)).toBe(true)
})

import { expect, test } from "bun:test"
import { packageLogSources } from "./package-logs"

const file = (path, label, lens) => ({
  id: `file:${path}`,
  label,
  kind: "system",
  path,
  lens,
  rotated: false,
})

const index = (...sources) => ({ sources, units: [], roots: ["/var/log"], missing: {} })

test("apt's transactions come first, then dpkg, then what ran unattended", () => {
  const sources = packageLogSources(
    index(
      file("/var/log/dpkg.log", "dpkg", "packages"),
      file(
        "/var/log/unattended-upgrades/unattended-upgrades-dpkg.log",
        "unattended-upgrades/unattended-upgrades-dpkg.log",
        "packages",
      ),
      file("/var/log/syslog", "syslog", "syslog"),
      file(
        "/var/log/unattended-upgrades/unattended-upgrades.log",
        "unattended-upgrades",
        "packages",
      ),
      file("/var/log/apt/history.log", "apt history", "packages"),
    ),
  )
  expect(sources.map((s) => s.label)).toEqual([
    "apt history",
    "dpkg",
    "unattended-upgrades",
    "unattended-upgrades/unattended-upgrades-dpkg.log",
  ])
})

test("each is read as a package log and drawn as the distribution", () => {
  const [source] = packageLogSources(index(file("/var/log/dnf.log", "dnf", "packages")), "fedora")
  expect(source.lens).toBe("packages")
  expect(source.product).toBe("fedora")
})

test("a host with no package log offers none", () => {
  expect(packageLogSources(index(file("/var/log/syslog", "syslog", "syslog")))).toEqual([])
})

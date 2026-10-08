import { describe, expect, test } from "bun:test"
import { healthInvestigation, storageCandidates } from "./server-advisor"

describe("host finding investigations", () => {
  test("every host check has an investigation and paths stay intact", () => {
    const ids = [
      "disk:/mnt/a:b",
      "inodes:/mnt/data",
      "memory",
      "swap",
      "steal",
      "load",
      "iowait",
      "psi-cpu",
      "psi-mem",
      "psi-io",
      "files",
      "timewait",
      "drops:tailscale0",
      "neterr:eth0",
      "temp:NVMe:1",
      "systemd.failed",
      "docker.unhealthy",
      "docker.restarting",
      "docker.dead",
      "docker.paused",
    ]
    for (const id of ids) expect(healthInvestigation(id)).toBeDefined()
    expect(healthInvestigation("disk:/mnt/a:b").path).toBe("/mnt/a:b")
    expect(healthInvestigation("temp:NVMe:1").kind).toBe("external")
    expect(healthInvestigation("future-check")).toBeUndefined()
  })
  test("failed services and containers are fixed in place rather than on another page", () => {
    expect(healthInvestigation("systemd.failed").kind).toBe("services")
    expect(healthInvestigation("docker.unhealthy").kind).toBe("containers")
    expect(healthInvestigation("neterr:eth0").networkInterface).toBe("eth0")
  })
  test("memory, swap and I/O lead to their own consumers", () => {
    expect(healthInvestigation("memory").sort).toBe("memory")
    expect(healthInvestigation("swap").sort).toBe("swap")
    expect(healthInvestigation("psi-io").sort).toBe("io")
    expect(healthInvestigation("files").sort).toBe("handles")
  })
})

test("copy cleanup retains the keeper and does not duplicate a temporary selection", () => {
  const keeper = { path: "/tmp/a", identity: "a" }
  const copy = { path: "/tmp/b", identity: "b" }
  const report = {
    temporaryFiles: [keeper, copy],
    duplicates: [{ files: [keeper, copy], sha256: "full-checksum" }],
  }
  const candidates = storageCandidates(report)
  expect(candidates).toEqual([{ kind: "duplicate", file: copy, keeper, sha256: "full-checksum" }])
})

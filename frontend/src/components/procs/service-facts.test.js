import { describe, expect, test } from "bun:test"
import {
  cpuQuotaPercent,
  environmentFiles,
  exposureTone,
  filterUnits,
  parseBytes,
  relatedHref,
  relations,
  serviceHref,
  serviceWarnings,
  splitEnvironment,
  startupOf,
} from "./service-facts"

describe("serviceHref", () => {
  test("names the unit's page and its view, keeping @ readable", () => {
    expect(serviceHref("nginx.service")).toBe("/processes/services/nginx.service")
    expect(serviceHref("getty@tty1.service", "journal")).toBe(
      "/processes/services/getty@tty1.service?tab=journal",
    )
    expect(serviceHref("nginx.service", "overview")).toBe("/processes/services/nginx.service")
  })
})

describe("relatedHref", () => {
  test("a service opens its page, a timer its schedule, a target nothing", () => {
    expect(relatedHref("redis.service")).toBe("/processes/services/redis.service")
    expect(relatedHref("certbot.timer")).toBe("/processes/scheduled?timer=certbot.timer")
    expect(relatedHref("multi-user.target")).toBeUndefined()
  })
})

describe("splitEnvironment", () => {
  test("reads systemd's quoting back into assignments", () => {
    expect(splitEnvironment('A=1 "B=has space" C=x')).toEqual(["A=1", "B=has space", "C=x"])
    expect(splitEnvironment('"Q=say \\"hi\\""')).toEqual(['Q=say "hi"'])
    expect(splitEnvironment("")).toEqual([])
    expect(splitEnvironment(undefined)).toEqual([])
  })
})

describe("environmentFiles", () => {
  test("says which files may be missing", () => {
    expect(environmentFiles("/etc/default/nginx (ignore_errors=yes)")).toEqual([
      { path: "/etc/default/nginx", optional: true },
    ])
    expect(environmentFiles("/a (ignore_errors=no) /b (ignore_errors=yes)")).toEqual([
      { path: "/a", optional: false },
      { path: "/b", optional: true },
    ])
  })
})

describe("cpuQuotaPercent", () => {
  test("reads the quota as a share of one core", () => {
    expect(cpuQuotaPercent("1.500000s")).toBe(150)
    expect(cpuQuotaPercent("500ms")).toBe(50)
    expect(cpuQuotaPercent("infinity")).toBeUndefined()
    expect(cpuQuotaPercent(undefined)).toBeUndefined()
  })
})

describe("parseBytes", () => {
  test("takes the sizes an operator types", () => {
    expect(parseBytes("512M")).toBe(512 * 1024 ** 2)
    expect(parseBytes("1.5G")).toBe(1.5 * 1024 ** 3)
    expect(parseBytes("2GiB")).toBe(2 * 1024 ** 3)
    expect(parseBytes("4096")).toBe(4096)
    expect(parseBytes("lots")).toBeUndefined()
    expect(parseBytes("0")).toBeUndefined()
  })
})

describe("relations", () => {
  test("keeps only the dependencies it names", () => {
    expect(relations({ Wants: "a.target b.service", After: "", Requires: "c.service" })).toEqual({
      Wants: ["a.target", "b.service"],
      Requires: ["c.service"],
    })
  })
})

describe("filterUnits", () => {
  const units = [
    {
      name: "nginx.service",
      description: "web",
      activeState: "active",
      unitFileState: "enabled",
      loadState: "loaded",
    },
    {
      name: "pg.service",
      description: "db",
      activeState: "failed",
      unitFileState: "enabled-runtime",
      loadState: "loaded",
    },
    {
      name: "gone.service",
      description: "",
      activeState: "inactive",
      unitFileState: "",
      loadState: "not-found",
    },
  ]
  test("narrows as the table does and leaves out what is not installed", () => {
    expect(filterUnits(units, { query: "", state: "", startup: "" }).map((u) => u.name)).toEqual([
      "nginx.service",
      "pg.service",
    ])
    expect(
      filterUnits(units, { query: "", state: "failed", startup: "" }).map((u) => u.name),
    ).toEqual(["pg.service"])
    expect(
      filterUnits(units, { query: "WEB", state: "", startup: "enabled" }).map((u) => u.name),
    ).toEqual(["nginx.service"])
    expect(startupOf(units[1])).toBe("enabled")
  })
})

describe("exposureTone", () => {
  test("colours the score as systemd's verdicts do", () => {
    expect(exposureTone(2.1)).toBe("running")
    expect(exposureTone(5)).toBe("notice")
    expect(exposureTone(7.4)).toBe("warning")
    expect(exposureTone(9.6)).toBe("danger")
  })
})

describe("serviceWarnings", () => {
  const unit = { name: "a.service", activeState: "active", memoryBytes: 950 }
  test("a changed unit file, a frozen cgroup and memory at its ceiling", () => {
    const keys = serviceWarnings(unit, {
      NeedDaemonReload: "yes",
      FreezerState: "frozen",
      MemoryMax: "1000",
    }).map((w) => w.key)
    expect(keys).toEqual(["reload", "frozen", "memory"])
  })
  test("a skipped condition only while it is not running", () => {
    expect(serviceWarnings(unit, { ConditionResult: "no" })).toEqual([])
    expect(
      serviceWarnings({ ...unit, activeState: "inactive" }, { ConditionResult: "no" }).map(
        (w) => w.key,
      ),
    ).toEqual(["condition"])
  })
})

import { expect, test } from "bun:test"
import {
  gpusWords,
  parseSysctls,
  parseTmpfs,
  parseUlimits,
  readOnlyStartingPaths,
  runtimeFlagsDraftOf,
  runtimeFlagsLine,
  runtimeFlagsOf,
} from "./runtime-flags-model"

const saved = {
  user: "1000:1000",
  readOnlyRootfs: true,
  tmpfs: [{ path: "/tmp", sizeMb: 128 }, { path: "/run" }],
  ulimits: [
    { name: "memlock", soft: -1, hard: -1 },
    { name: "nofile", soft: 1024, hard: 4096 },
  ],
  sysctls: [{ name: "kernel.sem", value: "250 32000 32 128" }],
  gpus: { mode: "devices", deviceIds: ["0", "1"], previews: true },
}

test("a saved plan reads back as the lines docker run takes, and saves as it was", () => {
  const draft = runtimeFlagsDraftOf(saved)
  expect(draft.tmpfsText).toBe("/tmp 128 MiB\n/run")
  expect(draft.ulimitsText).toBe("memlock=unlimited\nnofile=1024:4096")
  expect(draft.sysctlsText).toBe("kernel.sem=250 32000 32 128")
  expect(draft.gpuMode).toBe("devices")
  expect(draft.gpuDevices).toBe("0, 1")
  expect(runtimeFlagsOf(draft).plan).toEqual(saved)
})

test("nothing set is nothing saved, so turning a flag on and off is not an edit", () => {
  const draft = runtimeFlagsDraftOf({})
  expect(draft.gpuMode).toBe("none")
  expect(runtimeFlagsOf(draft).plan).toEqual({
    user: undefined,
    readOnlyRootfs: undefined,
    tmpfs: undefined,
    ulimits: undefined,
    sysctls: undefined,
    gpus: undefined,
  })
})

test("a limit is name=soft:hard, one value for both, -1 or unlimited for no limit", () => {
  expect(parseUlimits("NOFILE=65536\nmemlock = -1 : unlimited\n\ncore=0:-1").value).toEqual([
    { name: "nofile", soft: 65536, hard: 65536 },
    { name: "memlock", soft: -1, hard: -1 },
    { name: "core", soft: 0, hard: -1 },
  ])
  expect(parseUlimits("nofile 1024").refusal).toEqual({
    field: "ulimitsText",
    message: "“nofile 1024” is not a limit: write name=soft:hard, such as nofile=65536:65536.",
  })
  expect(parseUlimits("files=10").refusal?.message).toContain("files is not a limit Docker sets")
  expect(parseUlimits("nofile=lots").refusal?.field).toBe("ulimitsText")
})

test("a writable path is absolute, with an optional size in MiB", () => {
  expect(parseTmpfs("/tmp\n/var/cache/nginx 32 MiB\n/scratch 8m").value).toEqual([
    { path: "/tmp" },
    { path: "/var/cache/nginx", sizeMb: 32 },
    { path: "/scratch", sizeMb: 8 },
  ])
  expect(parseTmpfs("tmp").refusal?.field).toBe("tmpfsText")
  expect(parseTmpfs("/tmp big").refusal?.message).toContain("such as /tmp 128 MiB")
})

test("a kernel parameter is name=value, the value as written", () => {
  expect(parseSysctls("net.core.somaxconn = 1024\nkernel.sem=250 32000 32 128").value).toEqual([
    { name: "net.core.somaxconn", value: "1024" },
    { name: "kernel.sem", value: "250 32000 32 128" },
  ])
  expect(parseSysctls("net.core.somaxconn").refusal?.message).toContain("name=value")
})

test("the GPU choice saves as the server's request, and refuses a count or list it lacks", () => {
  const base = runtimeFlagsDraftOf({})
  expect(runtimeFlagsOf({ ...base, gpuMode: "all" }).plan?.gpus).toEqual({
    mode: "all",
    previews: undefined,
  })
  expect(runtimeFlagsOf({ ...base, gpuMode: "count", gpuCount: "2" }).plan?.gpus).toEqual({
    mode: "count",
    count: 2,
    previews: undefined,
  })
  expect(runtimeFlagsOf({ ...base, gpuMode: "count", gpuCount: "" }).refusal?.field).toBe(
    "gpuCount",
  )
  expect(
    runtimeFlagsOf({ ...base, gpuMode: "devices", gpuDevices: "0 GPU-3a1b", gpuPreviews: true })
      .plan?.gpus,
  ).toEqual({ mode: "devices", deviceIds: ["0", "GPU-3a1b"], previews: true })
  expect(runtimeFlagsOf({ ...base, gpuMode: "devices", gpuDevices: " " }).refusal?.field).toBe(
    "gpuDevices",
  )
  expect(gpusWords({ mode: "all" })).toBe("every GPU")
  expect(gpusWords({ mode: "count", count: 2 })).toBe("2 GPUs")
  expect(gpusWords({ mode: "devices", deviceIds: ["1"] })).toBe("GPU 1")
  expect(gpusWords(undefined)).toBeUndefined()
})

test("a read-only root starts with /tmp, and where nginx writes for a static site", () => {
  expect(readOnlyStartingPaths("dockerfile")).toEqual(["/tmp"])
  expect(readOnlyStartingPaths("static")).toEqual(["/tmp", "/var/cache/nginx", "/run"])
  expect(readOnlyStartingPaths("recipe", "dist")).toEqual(["/tmp", "/var/cache/nginx", "/run"])
  expect(readOnlyStartingPaths("recipe", "")).toEqual(["/tmp"])
})

test("the section head says what the flags add up to", () => {
  expect(runtimeFlagsLine(saved)).toBe(
    "runs as 1000:1000 · read-only root · 2 ulimits · 1 kernel parameter",
  )
  expect(runtimeFlagsLine({})).toBeUndefined()
})

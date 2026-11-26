import { expect, test } from "bun:test"
import {
  accessCounts,
  accessSentence,
  addressPort,
  canConfigure,
  chosenPreset,
  configureRefusal,
  configureVerb,
  failedSteps,
  footprintWords,
  memoryWords,
  orderedActions,
  presetServices,
  readinessLines,
  serviceState,
  skippedLine,
  splitCredentials,
  stackPollInterval,
  stageGroups,
  stepDetail,
  tunnelFor,
} from "./stack-model"

const entry = {
  presets: [
    { id: "essential", services: ["jellyfin", "seerr"], memoryMb: 3584, diskGb: 4 },
    {
      id: "complete",
      services: ["jellyfin", "seerr", "radarr", "recyclarr", "gone"],
      recommended: true,
      memoryMb: 7424,
      diskGb: 12,
    },
  ],
  services: [
    { name: "radarr", access: "private" },
    { name: "jellyfin", access: "public" },
    { name: "seerr", access: "private" },
    { name: "recyclarr", access: "worker" },
  ],
}

test("a preset's services come in its own order, and one the catalogue does not know is left out", () => {
  const services = presetServices(entry, entry.presets[1])
  expect(services.map((service) => service.name)).toEqual([
    "jellyfin",
    "seerr",
    "radarr",
    "recyclarr",
  ])
  expect(accessCounts(services)).toBe("1 public, 2 private, 1 background")
  expect(accessCounts(presetServices(entry, entry.presets[0]))).toBe("1 public, 1 private")
  expect(accessSentence(presetServices(entry, entry.presets[0]))).toBe(
    "1 public, 1 private: public is on the project's address, private is on this server only (127.0.0.1).",
  )
  expect(accessSentence(services)).toMatch(/background has no web interface\.$/)
  expect(accessSentence([])).toBe("")
})

test("the tab opens on the preset asked for, else the recommended one", () => {
  expect(chosenPreset(entry, "essential").id).toBe("essential")
  expect(chosenPreset(entry, undefined).id).toBe("complete")
  expect(chosenPreset(entry, "ultimate").id).toBe("complete")
  expect(chosenPreset({ presets: [{ id: "only" }] }, undefined).id).toBe("only")
  expect(chosenPreset({ presets: [] }, undefined)).toBeUndefined()
})

test("memory is megabytes under a gigabyte and gigabytes over it, and media is not in the estimate", () => {
  expect(memoryWords(512)).toBe("512 MB")
  expect(memoryWords(1024)).toBe("1 GB")
  expect(memoryWords(7424)).toBe("7.3 GB")
  expect(footprintWords(entry.presets[1])).toBe(
    "Memory limits add up to 7.3 GB, and the applications' own data takes about 12 GB of disk. Your media takes what you add on top.",
  )
})

test("the page asks every two seconds while a release or a pass is moving", () => {
  expect(stackPollInterval("waiting")).toBe(2000)
  expect(stackPollInterval("configuring")).toBe(2000)
  expect(stackPollInterval("ready")).toBe(15000)
  expect(stackPollInterval("action_required")).toBe(15000)
  expect(stackPollInterval("failed")).toBe(15000)
  expect(stackPollInterval(undefined)).toBe(15000)
})

test("readiness is five separate facts, and only the first is said before a release is live", () => {
  expect(
    readinessLines({
      installed: true,
      healthy: true,
      connected: true,
      acquisition: false,
      playback: true,
    }),
  ).toEqual([
    { key: "installed", met: true, label: "Installed" },
    { key: "healthy", met: true, label: "Every service healthy" },
    { key: "connected", met: true, label: "Services connected" },
    { key: "acquisition", met: false, label: "Cannot fetch new titles" },
    { key: "playback", met: true, label: "Can play your library" },
  ])
  expect(
    readinessLines({
      installed: false,
      healthy: false,
      connected: false,
      acquisition: false,
      playback: false,
    }),
  ).toEqual([{ key: "installed", met: false, label: "Not installed" }])
})

test("blocking actions come first, each group in the server's order", () => {
  const actions = [
    { id: "optional-a", blocking: false },
    { id: "source", blocking: true },
    { id: "optional-b", blocking: false },
    { id: "owner", blocking: true },
  ]
  expect(orderedActions(actions).map((action) => action.id)).toEqual([
    "source",
    "owner",
    "optional-a",
    "optional-b",
  ])
})

const steps = [
  { id: "jellyfin.setup", stage: "initialize", status: "done", attempts: 1, message: "Created" },
  { id: "radarr.auth", stage: "initialize", status: "skipped", message: "Not in this preset." },
  { id: "seerr.setup", stage: "link", status: "running", attempts: 3 },
  { id: "seerr.managers", stage: "link", status: "skipped", message: "Not in this preset" },
  { id: "seerr.policy", stage: "link", status: "pending", attempts: 0 },
  { id: "downloads.verify", stage: "verify", status: "skipped", message: "Not in this preset" },
]

test("steps group by stage in the order a pass runs, without the skipped and without empty stages", () => {
  const groups = stageGroups(steps)
  expect(
    groups.map((group) => [group.stage, group.steps.map((step) => step.id), group.done]),
  ).toEqual([
    ["initialize", ["jellyfin.setup"], 1],
    ["link", ["seerr.setup", "seerr.policy"], 0],
  ])
  expect(groups[0].title).toBe("Setting up each application")
  expect(stageGroups([])).toEqual([])
})

test("skipped steps are one line, by reason", () => {
  expect(skippedLine(steps)).toBe("3 steps skipped: Not in this preset.")
  expect(
    skippedLine([
      { status: "skipped", message: "Radarr is not in this preset" },
      { status: "skipped", message: "Radarr is not in this preset" },
      { status: "skipped", message: "Blocked by qbittorrent.auth" },
      { status: "skipped" },
    ]),
  ).toBe(
    "4 steps skipped: Radarr is not in this preset (2); Blocked by qbittorrent.auth (1); no reason given (1).",
  )
  expect(skippedLine([{ status: "skipped", message: "Seerr is absent" }])).toBe(
    "1 step skipped: Seerr is absent.",
  )
  expect(skippedLine([{ status: "done" }])).toBeUndefined()
})

test("a failed step says its error, and a retried one how often it has tried", () => {
  const failed = {
    status: "failed",
    attempts: 5,
    message: "Signing in",
    error: "401 from /api/v2/auth/login",
  }
  expect(failedSteps([failed, { status: "done" }])).toEqual([failed])
  expect(stepDetail(failed)).toBe("401 from /api/v2/auth/login")
  expect(stepDetail({ status: "failed", attempts: 1 })).toBe("Failed with no error recorded")
  expect(stepDetail({ status: "running", attempts: 3 })).toBe("Attempt 3")
  expect(stepDetail({ status: "running", attempts: 1 })).toBeUndefined()
  expect(stepDetail({ status: "done", attempts: 1, message: "Created 2 libraries" })).toBe(
    "Created 2 libraries",
  )
})

test("a service's state is its setup's when that needs saying, else its container's", () => {
  const running = { name: "c", state: "running", health: "healthy" }
  expect(serviceState({ status: "ok", container: running })).toEqual({
    tone: "running",
    label: "Healthy",
  })
  expect(serviceState({ status: "ok", container: { name: "c", state: "running" } }).label).toBe(
    "Running",
  )
  expect(serviceState({ status: "configuring", container: running }).label).toBe("Being set up")
  expect(serviceState({ status: "failed", container: running })).toEqual({
    tone: "danger",
    label: "Setup failed",
  })
  expect(serviceState({ status: "action_required", container: running }).label).toBe("Needs you")
  expect(serviceState({ status: "waiting" })).toEqual({ tone: "unknown", label: "No container" })
  expect(
    serviceState({
      status: "configuring",
      container: { name: "c", state: "running", health: "starting" },
    }),
  ).toEqual({ tone: "notice", label: "Starting" })
  expect(
    serviceState({ status: "ok", container: { name: "c", state: "running", health: "unhealthy" } }),
  ).toEqual({ tone: "danger", label: "Unhealthy" })
  expect(serviceState({ status: "ok", container: { name: "c", state: "exited" } })).toEqual({
    tone: "danger",
    label: "Exited",
  })
  expect(serviceState({ status: "ok", container: { name: "c", state: "restarting" } }).tone).toBe(
    "warning",
  )
  expect(serviceState({ status: "ok", container: { name: "c", state: "paused" } })).toEqual({
    tone: "stopped",
    label: "Paused",
  })
})

test("a private address becomes a tunnel to its own port, and nothing without a port", () => {
  expect(addressPort("127.0.0.1:23950")).toBe(23950)
  expect(addressPort("[::1]:8080")).toBe(8080)
  expect(addressPort("127.0.0.1")).toBeUndefined()
  expect(addressPort("127.0.0.1:99999")).toBeUndefined()
  expect(addressPort(undefined)).toBeUndefined()
  expect(tunnelFor("127.0.0.1:23950", "dash.example.test")).toEqual({
    command: "ssh -L 23950:127.0.0.1:23950 user@dash.example.test",
    url: "http://127.0.0.1:23950",
  })
  expect(tunnelFor("127.0.0.1:23950", "").command).toBe(
    "ssh -L 23950:127.0.0.1:23950 user@your-server",
  )
  expect(tunnelFor(undefined, "dash.example.test")).toBeUndefined()
})

test("only an account that exists can be revealed", () => {
  const { stored, pending } = splitCredentials([
    { name: "jellyfin-admin", stored: true },
    { name: "radarr-admin", stored: false },
    { name: "sonarr-admin", stored: false },
  ])
  expect(stored.map((credential) => credential.name)).toEqual(["jellyfin-admin"])
  expect(pending).toBe(2)
})

test("a refused pass is said in words, and the button's verb follows the state", () => {
  expect(configureRefusal("stack_busy")).toMatch(/already running/)
  expect(configureRefusal("stack_waiting")).toMatch(/no live release yet/)
  expect(configureRefusal("forbidden")).toBeUndefined()
  expect(configureRefusal(undefined)).toBeUndefined()
  expect(configureVerb("failed").label).toBe("Retry")
  expect(configureVerb("ready").label).toBe("Repair")
  expect(configureVerb("action_required").label).toBe("Repair")
  expect(canConfigure({ status: "waiting" })).toBe(false)
  expect(canConfigure({ status: "configuring" })).toBe(false)
  expect(canConfigure({ status: "failed" })).toBe(true)
  expect(canConfigure({ status: "ready" })).toBe(true)
})

import { expect, test } from "bun:test"
import {
  addressSentence,
  addressesRefusal,
  addressesSentence,
  onlyUnpublished,
  appReach,
  canConfigure,
  chosenPreset,
  configureRefusal,
  configureVerb,
  failedSteps,
  footprintWords,
  isSourceAction,
  listWords,
  loginPasswordProblem,
  memoryWords,
  missingHostnames,
  nextSteps,
  orderedActions,
  plannedAddresses,
  presetServices,
  readinessLines,
  serviceHostname,
  serviceState,
  skippedLine,
  stackPollInterval,
  stageGroups,
  stepDetail,
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
    { name: "radarr", title: "Radarr", access: "private", address: "radarr" },
    { name: "jellyfin", title: "Jellyfin", access: "public", primary: true },
    { name: "seerr", title: "Seerr", access: "private", address: "requests" },
    { name: "recyclarr", title: "Recyclarr", access: "worker" },
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
  expect(addressSentence(presetServices(entry, entry.presets[0]))).toBe(
    "Every app answers at its own HTTPS address beside the domain, and they all sign in with one password.",
  )
  expect(addressSentence(services)).toMatch(
    /one password\. Recyclarr runs in the background with no web interface\.$/,
  )
  expect(addressSentence([{ title: "Jellyfin", access: "public" }])).toBe(
    "Jellyfin answers at its own HTTPS address and signs in with the stack's one password.",
  )
  expect(addressSentence([{ title: "Recyclarr", access: "worker" }])).toBe("")
  expect(addressSentence([])).toBe("")
})

test("an app's address is a sibling of the domain's first label, as the server makes it", () => {
  expect(serviceHostname("media.example.com", "requests")).toBe("media-requests.example.com")
  expect(serviceHostname("Media.Example.com.", "radarr")).toBe("media-radarr.example.com")
  expect(serviceHostname("  media.home.example.co.uk ", "sonarr")).toBe(
    "media-sonarr.home.example.co.uk",
  )
  // An apex has no first label to join: the label goes in front.
  expect(serviceHostname("example.com", "requests")).toBe("requests.example.com")
  expect(serviceHostname("localhost", "requests")).toBe("requests.localhost")
  // The just-dashboard.com name and sslip.io are covered the same way.
  expect(serviceHostname("media.acme.just-dashboard.com", "downloads")).toBe(
    "media-downloads.acme.just-dashboard.com",
  )
  expect(serviceHostname("media-server-a1b2.203-0-113-7.sslip.io", "subtitles")).toBe(
    "media-server-a1b2-subtitles.203-0-113-7.sslip.io",
  )
  // A first label that would make one longer than 63 is cut from its end.
  const long = `${"a".repeat(60)}.example.com`
  const cut = serviceHostname(long, "requests")
  expect(cut.split(".")[0]).toBe(`${"a".repeat(54)}-requests`)
  expect(cut.split(".")[0]).toHaveLength(63)
  expect(serviceHostname(`${"b".repeat(54)}.example.com`, "requests").split(".")[0]).toHaveLength(
    63,
  )
  // A cut that ends on a hyphen leaves it out, as the server's rule does.
  expect(serviceHostname(`${"a".repeat(53)}-${"b".repeat(10)}.example.com`, "requests")).toBe(
    `${"a".repeat(53)}-requests.example.com`,
  )
  expect(serviceHostname("", "requests")).toBe("")
  expect(serviceHostname("media.example.com", "")).toBe("")
})

test("the Stacks tab shows the address each app gets, the primary on the domain itself", () => {
  const services = presetServices(entry, entry.presets[1])
  expect(plannedAddresses(services, "media.example.com")).toEqual([
    { service: "jellyfin", title: "Jellyfin", hostname: "media.example.com" },
    { service: "seerr", title: "Seerr", hostname: "media-requests.example.com" },
    { service: "radarr", title: "Radarr", hostname: "media-radarr.example.com" },
  ])
  expect(plannedAddresses(services, "  ")).toEqual([])
})

test("lists of names read as a sentence", () => {
  expect(listWords([])).toBe("")
  expect(listWords(["Seerr"])).toBe("Seerr")
  expect(listWords(["Seerr", "Radarr"])).toBe("Seerr and Radarr")
  expect(listWords(["Seerr", "Radarr", "Sonarr"])).toBe("Seerr, Radarr and Sonarr")
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

test("an app is opened at its address, a worker runs in the background, and none is not a link", () => {
  expect(appReach({ access: "public", url: "https://media.example.com" })).toEqual({
    kind: "open",
    url: "https://media.example.com",
  })
  expect(
    appReach({
      access: "private",
      url: "https://media-requests.example.com",
      address: "127.0.0.1:28055",
    }),
  ).toEqual({ kind: "open", url: "https://media-requests.example.com" })
  expect(appReach({ access: "private", address: "127.0.0.1:28055" })).toEqual({
    kind: "none",
    detail: "Published on this server at 127.0.0.1:28055",
  })
  expect(appReach({ access: "private" })).toEqual({ kind: "none", detail: undefined })
  expect(appReach({ access: "worker", address: "127.0.0.1:1" })).toEqual({ kind: "background" })
})

const missing = {
  primary: "media.example.com",
  missing: [
    { service: "seerr", title: "Seerr", hostname: "media-requests.example.com" },
    { service: "radarr", title: "Radarr", hostname: "media-radarr.example.com" },
  ],
}

test("the next steps are what holds the stack from ready, then the apps with no address, then the rest", () => {
  const actions = [
    { id: "seerr-people", blocking: false },
    { id: "media-source", blocking: true },
  ]
  expect(
    nextSteps({ status: "action_required", actions, addresses: missing }).map((step) =>
      step.kind === "action" ? step.action.id : step.kind,
    ),
  ).toEqual(["media-source", "addresses", "seerr-people"])
  // Every app has its address: nothing to give.
  expect(
    nextSteps({
      status: "ready",
      actions: [],
      addresses: { primary: "m.example.com", missing: [] },
    }),
  ).toEqual([])
  // Before a release is live nothing is offered for the addresses.
  expect(nextSteps({ status: "waiting", actions: [], addresses: missing })).toEqual([])
  // A server older than the addresses says nothing of them.
  expect(nextSteps({ status: "ready", actions: [] })).toEqual([])
  expect(isSourceAction({ id: "media-source" })).toBe(true)
  expect(isSourceAction({ id: "seerr-people" })).toBe(false)
})

test("the apps with no address are named, with where theirs go", () => {
  expect(addressesSentence(missing)).toBe(
    "Seerr and Radarr have no address yet. Each gets one beside media.example.com, and a deployment starts to serve them.",
  )
  expect(addressesSentence({ ...missing, missing: missing.missing.slice(0, 1) })).toBe(
    "Seerr has no address yet. It gets one beside media.example.com, and a deployment starts to serve it.",
  )
  expect(addressesSentence({ primary: "", missing: missing.missing })).toMatch(
    /^Seerr and Radarr have no address yet, and neither has the stack: give it a domain/,
  )
  // With no primary, the names are made from the domain typed in the dialog.
  expect(
    missingHostnames(
      [
        { service: "seerr", title: "Seerr", hostname: "" },
        { service: "bazarr", title: "Bazarr", hostname: "" },
        { service: "radarr", title: "Radarr", hostname: "media-radarr.example.com" },
      ],
      { seerr: "requests" },
      "tv.example.org",
    ),
  ).toEqual([
    { service: "seerr", title: "Seerr", hostname: "tv-requests.example.org" },
    { service: "bazarr", title: "Bazarr", hostname: "" },
    { service: "radarr", title: "Radarr", hostname: "media-radarr.example.com" },
  ])
  expect(addressesRefusal("stack_addresses_present")).toBe("Every app already has its address.")
  expect(addressesRefusal("stack_no_domain")).toMatch(/no domain yet/)
  expect(addressesRefusal("forbidden")).toBeUndefined()
})

test("a new password for every app is held to the dashboard's own rule", () => {
  expect(loginPasswordProblem("")).toBe("Type the new password.")
  expect(loginPasswordProblem("Short1!")).toBe("Use at least 12 characters.")
  expect(loginPasswordProblem("alllowercaseletters")).toMatch(/^Mix three/)
  expect(loginPasswordProblem("Lower-and-UPPER")).toBeUndefined()
  expect(loginPasswordProblem("fixture-Every-app-0001")).toBeUndefined()
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

test("addresses saved but not served are offered again, in words", () => {
  const saved = { primary: "media.example.com", missing: [], unpublished: missing.missing }
  expect(onlyUnpublished(saved)).toBe(true)
  expect(onlyUnpublished(missing)).toBe(false)
  expect(onlyUnpublished({ primary: "m.example.com", missing: [] })).toBe(false)
  expect(
    nextSteps({ status: "action_required", actions: [], addresses: saved }).map(
      (step) => step.kind,
    ),
  ).toEqual(["addresses"])
  expect(addressesSentence(saved)).toBe(
    "Seerr and Radarr have their addresses saved, but the release that was to publish them did not finish. Deploying again publishes them.",
  )
  expect(addressesSentence({ ...saved, unpublished: missing.missing.slice(0, 1) })).toBe(
    "Seerr has its address saved, but the release that was to publish it did not finish. Deploying again publishes it.",
  )
})

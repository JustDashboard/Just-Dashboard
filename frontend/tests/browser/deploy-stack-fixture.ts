import type { Page, Route } from "@playwright/test"
import type {
  BlueprintDetail,
  BlueprintInput,
  BlueprintSummary,
  DeploymentRuntimeServices,
  SmartStackCatalogueEntry,
  SmartStackSource,
  SmartStackSourceDefinition,
  SmartStackSourceDetail,
  SmartStackState,
  SmartStackStatus,
  SmartStackStep,
} from "../../src/lib/types"
import {
  blueprintCatalogue,
  deployment,
  json,
  mockNewProject,
  mockProject,
  now,
  project,
  run,
} from "./deploy-fixture"

/**
 * The media stack, mocked: its catalogue entry, its two preset templates, a
 * stack project's state in each of the five states the server gives it, and
 * its media sources. The shapes are the stack API contract's
 * (`GET /deploy/stacks`, `GET /deploy/{id}/stack`, `POST …/stack/configure`,
 * `PUT …/stack/login`, `POST …/stack/addresses`, `GET …/reveal` and the
 * `…/stack/sources` routes), and the words are the manifest's own, so a spec
 * reads like the product.
 */

const SERVICES = [
  {
    name: "jellyfin",
    title: "Jellyfin",
    purpose: "Plays your films and series, with apps for every screen",
    port: 8096,
    access: "public",
    docsUrl: "https://jellyfin.org/docs/",
    image: "jellyfin/jellyfin:12.4",
    primary: true,
  },
  {
    name: "seerr",
    title: "Seerr",
    purpose: "Where people search for a title and ask for it",
    port: 5055,
    access: "private",
    docsUrl: "https://docs.seerr.dev/",
    image: "ghcr.io/seerr-team/seerr:v3.5.2",
    address: "requests",
  },
  {
    name: "radarr",
    title: "Radarr",
    purpose: "Finds, downloads and files films",
    port: 7878,
    access: "private",
    image: "lscr.io/linuxserver/radarr:7.0.3.10918-ls328",
    address: "radarr",
  },
  {
    name: "sonarr",
    title: "Sonarr",
    purpose: "Finds, downloads and files series",
    port: 8989,
    access: "private",
    image: "lscr.io/linuxserver/sonarr:4.0.20.3014-ls6",
    address: "sonarr",
  },
  {
    name: "prowlarr",
    title: "Prowlarr",
    purpose: "Keeps the sources you add and shares them with Radarr and Sonarr",
    port: 9696,
    access: "private",
    image: "lscr.io/linuxserver/prowlarr:3.1.0.5799-ls170",
    address: "prowlarr",
  },
  {
    name: "qbittorrent",
    title: "qBittorrent",
    purpose: "Downloads what Radarr and Sonarr send it",
    port: 8080,
    access: "private",
    image: "lscr.io/linuxserver/qbittorrent:5.3.1_v2.1.3-ls490",
    address: "downloads",
  },
  {
    name: "bazarr",
    title: "Bazarr",
    purpose: "Fetches subtitles in your languages",
    port: 6767,
    access: "private",
    image: "lscr.io/linuxserver/bazarr:v1.6.2-ls374",
    address: "subtitles",
  },
  {
    name: "recyclarr",
    title: "Recyclarr",
    purpose: "Keeps Radarr's and Sonarr's quality profiles in step with your choice",
    access: "worker",
    image: "recyclarr/recyclarr:8.8.1",
  },
] as const satisfies SmartStackCatalogueEntry["services"]

const ESSENTIAL = ["jellyfin", "seerr"]

const NO_LIBRARY_MOUNT =
  "Media goes into the stack's own volume. An existing library elsewhere on this server is not mounted."

export const COMPLETE_LIMITATIONS = [
  "Nothing can be downloaded until you add a source you are allowed to use. None is included, and no films or series come with the stack.",
  NO_LIBRARY_MOUNT,
]

export const ESSENTIAL_LIMITATIONS = [
  "A request cannot be fulfilled automatically: there is no manager or download client. Requests wait for you to add the title to the library yourself.",
  NO_LIBRARY_MOUNT,
]

export const stackCatalogue: SmartStackCatalogueEntry[] = [
  {
    id: "media",
    version: "1.1.0",
    title: "Media server",
    description:
      "Jellyfin to watch your films and series, and Seerr for people to ask for more. The Complete preset adds the managers, download client, subtitles and quality profiles that fetch what was asked for, from sources you add yourself.",
    presets: [
      {
        id: "complete",
        title: "Complete",
        description:
          "Everything connected: requests in Seerr are searched for by Radarr and Sonarr through the sources you add to Prowlarr, downloaded by qBittorrent, given subtitles by Bazarr and quality profiles by Recyclarr, and land in Jellyfin.",
        templateId: "media-complete",
        templateVersion: "1.1.0",
        services: SERVICES.map((service) => service.name),
        recommended: true,
        limitations: COMPLETE_LIMITATIONS,
        memoryMb: 7424,
        diskGb: 12,
      },
      {
        id: "essential",
        title: "Essential",
        description:
          "Jellyfin and Seerr for a library you already have. People can browse and ask for titles, but nothing fetches what they ask for.",
        templateId: "media-essential",
        templateVersion: "1.1.0",
        services: ESSENTIAL,
        limitations: ESSENTIAL_LIMITATIONS,
        memoryMb: 3584,
        diskGb: 4,
      },
    ],
    services: [...SERVICES],
  },
]

const ACCESS = {
  kind: "managed",
  note: "The dashboard finishes every application's first-run setup itself, before anyone else can reach it, and signs every one in with one login: admin and a password it keeps. Reveal it, or change it, on the stack's page.",
} as const

function summary(id: string, name: string, names: readonly string[]): BlueprintSummary {
  const services = SERVICES.filter((service) => names.includes(service.name))
  return {
    id,
    version: "1.1.0",
    name,
    stack: "media",
    category: "http",
    profile: "compose",
    description: "Set up and connected to each other by the dashboard.",
    iconId: "globe",
    docsUrl: "https://jellyfin.org/docs/",
    license: "GPL-2.0",
    maintainer: "Just Dashboard",
    reviewedAt: "2026-11-26",
    access: ACCESS,
    image: "jellyfin/jellyfin:12.4",
    memoryMb: id === "media-complete" ? 7424 : 3584,
    sourceKind: "compose",
    deploymentSupported: true,
    services: services.map((service) => ({
      name: service.name,
      role: service.access === "worker" ? "worker" : "app",
      purpose: service.purpose,
      image: service.image,
      memoryMb: 512,
      public: service.access === "public",
    })),
  }
}

/** The two presets as the blueprint catalogue lists them, each naming its stack. */
export const mediaSummaries: BlueprintSummary[] = [
  summary(
    "media-complete",
    "Media server (Complete)",
    SERVICES.map((service) => service.name),
  ),
  summary("media-essential", "Media server (Essential)", ESSENTIAL),
]

const DOMAIN_INPUT: BlueprintInput = {
  name: "domain",
  kind: "domain",
  label: "Domain",
  required: true,
  description: "Jellyfin answers on it, and every other app gets an address beside it.",
}

const LOGIN_INPUT: BlueprintInput = {
  name: "login-password",
  kind: "secret",
  label: "Password for every app",
  variable: "MEDIA_LOGIN_PASSWORD",
  required: false,
  description:
    "Optional. Leave it empty and the dashboard makes one; the stack's page shows it and can change it.",
}

const APPROVAL_INPUT: BlueprintInput = {
  name: "request-approval",
  kind: "choice",
  label: "Requests",
  variable: "MEDIA_REQUEST_APPROVAL",
  default: "admin",
  required: true,
  description: "Who decides what is fetched. People you let in to Seerr can always ask.",
  choices: [
    {
      value: "admin",
      label: "You approve each request",
      description: "Nothing is fetched until an administrator approves it",
    },
    {
      value: "auto",
      label: "Approve automatically",
      description: "A request from anyone you let in is fetched at once",
    },
  ],
}

function definition(
  listed: BlueprintSummary,
  inputs: NonNullable<BlueprintDetail["inputs"]>,
): BlueprintDetail {
  const names = (listed.services ?? []).map((service) => service.name)
  return {
    id: listed.id,
    version: listed.version,
    name: listed.name,
    stack: listed.stack,
    category: listed.category,
    profile: listed.profile,
    description: listed.description,
    iconId: listed.iconId,
    docsUrl: listed.docsUrl,
    access: listed.access,
    deploymentSupported: true,
    image: { reference: "", tagPolicy: "" },
    provenance: {
      maintainer: "Just Dashboard",
      license: "GPL-2.0",
      upstreamUrl: "https://github.com/jellyfin/jellyfin",
      reviewedAt: "2026-11-26",
      minimumDashboard: "0.9.1",
    },
    resources: { memoryMb: 0, minMemoryMb: 0 },
    update: { detector: "registry", backupFirst: true, notes: "" },
    inputs,
    services: SERVICES.filter((service) => names.includes(service.name)).map((service) => ({
      name: service.name,
      role: service.access === "worker" ? "worker" : "app",
      purpose: service.purpose,
      image: { reference: service.image, tagPolicy: "pinned" },
      ports:
        "port" in service
          ? [
              {
                name: "http",
                internal: service.port,
                protocol: "tcp",
                purpose: "Web interface",
                exposure: service.access === "public" ? "proxy" : "loopback",
              },
            ]
          : [],
      resources: { memoryMb: 512, minMemoryMb: 256 },
    })),
  }
}

export const mediaCompleteDefinition = definition(mediaSummaries[0], [
  DOMAIN_INPUT,
  LOGIN_INPUT,
  {
    name: "quality",
    kind: "choice",
    label: "Quality",
    variable: "MEDIA_QUALITY",
    default: "1080p",
    required: true,
    description: "What Radarr and Sonarr look for. Higher needs more disk and a faster connection.",
    choices: [
      { value: "720p", label: "720p", description: "Smallest files" },
      { value: "1080p", label: "1080p", description: "Full HD, the usual choice" },
      { value: "2160p", label: "4K", description: "Large files" },
    ],
  },
  {
    name: "subtitle-languages",
    kind: "text",
    label: "Subtitle languages",
    variable: "MEDIA_SUBTITLE_LANGUAGES",
    default: "en",
    required: false,
    pattern: "^$|^[a-z]{2}(,[a-z]{2}){0,9}$",
    description: "Two-letter codes, separated by commas (en,fr).",
  },
  APPROVAL_INPUT,
])

export const mediaEssentialDefinition = definition(mediaSummaries[1], [
  DOMAIN_INPUT,
  LOGIN_INPUT,
  APPROVAL_INPUT,
])

/**
 * The plan the server renders from a preset and its inputs, as a draft
 * holds it: a Compose build, the password's variable declared with no value,
 * and the domain's names, Jellyfin on the domain and each other app on its
 * sibling, routed to its own service.
 */
export function stackRenderedConfiguration(
  template: BlueprintDetail,
  inputs: Record<string, string> = {},
) {
  const domain = (inputs.domain ?? "").trim().toLowerCase()
  const names = new Set((template.services ?? []).map((service) => service.name))
  const labelled = SERVICES.filter((service) => "address" in service && names.has(service.name))
  const sibling = (label: string) => {
    const [first, ...rest] = domain.split(".")
    return rest.length >= 2 ? [`${first}-${label}`, ...rest].join(".") : `${label}.${domain}`
  }
  return {
    build: { method: "compose", noCache: false, secrets: [], releaseTasks: [] },
    runtime: {
      image: "",
      command: [],
      internalPort: 8096,
      hostPort: 0,
      bindAddress: "127.0.0.1",
      strategy: "stop_first",
      privileged: false,
      hostNetwork: false,
      capabilities: [],
      devices: [],
      memoryMb: template.id === "media-complete" ? 7424 : 3584,
      mounts: [],
    },
    variables: [
      {
        name: "MEDIA_LOGIN_PASSWORD",
        sensitivity: "secret",
        scopes: ["runtime"],
        required: false,
      },
    ],
    dependencies: [],
    checks: [],
    domains: domain
      ? [
          { hostname: domain, https: true, ownership: "managed" },
          ...labelled.map((service) => ({
            hostname: sibling("address" in service ? service.address : ""),
            https: true,
            ownership: "managed",
            service: service.name,
          })),
        ]
      : [],
  }
}

/**
 * `/deploy/new` with the stack catalogue, both preset templates listed among
 * the ordinary ones, and a draft whose detection answers for a rendered
 * Compose template. Returns the journey, and the sources the page saved.
 */
export async function mockNewStack(page: Page) {
  const journey = await mockNewProject(page)
  const sources: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/stacks", (route) => json(route, stackCatalogue))
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(route, [...blueprintCatalogue, ...mediaSummaries]),
  )
  await page.route("**/api/v1/deploy/blueprints/media-complete", (route) =>
    json(route, mediaCompleteDefinition),
  )
  await page.route("**/api/v1/deploy/blueprints/media-essential", (route) =>
    json(route, mediaEssentialDefinition),
  )
  await page.route("**/api/v1/deploy/drafts/journey-draft", (route) => {
    const body = route.request().postDataJSON() as { source?: Record<string, unknown> } | null
    if (route.request().method() === "PUT" && body?.source) sources.push(body.source)
    return route.fallback()
  })
  // The journey's own detection answers for a repository or an image. A
  // rendered Compose template is answered here, over the draft the journey
  // holds, which the page is asked for so the two cannot disagree.
  await page.route("**/api/v1/deploy/drafts/journey-draft/detect", async (route) => {
    const saved = sources.at(-1)
    if (saved?.kind !== "compose" || saved.mode !== "blueprint") return route.fallback()
    const held = (await page.evaluate(() =>
      fetch("/api/v1/deploy/drafts/journey-draft").then((response) => response.json()),
    )) as { revision: number; data: Record<string, unknown> }
    const template =
      saved.blueprintId === "media-essential" ? mediaEssentialDefinition : mediaCompleteDefinition
    const services = template.services ?? []
    return json(route, {
      ...held,
      currentStep: "detection",
      revision: held.revision + 1,
      data: {
        ...held.data,
        configuration: stackRenderedConfiguration(
          template,
          (saved.blueprintInputs ?? {}) as Record<string, string>,
        ),
        detection: {
          source: {
            kind: "compose",
            ref: `${template.id}@${template.version}`,
            composeFiles: ["compose.yaml"],
            services: services.map((service) => service.name),
          },
          candidates: [
            {
              id: "compose-candidate",
              name: `${template.name} (${services.length} services)`,
              root: "",
              profile: "compose",
              buildMethod: "compose",
              confidence: "high",
              evidence: [
                {
                  path: `${template.id}@${template.version}`,
                  reason: "reviewed template shipped with this dashboard",
                },
              ],
              needsDecision: [],
            },
          ],
          compose: {
            digest: `sha256:${"5".repeat(64)}`,
            files: ["compose.yaml"],
            services: services.map((service) => ({
              name: service.name,
              image: service.image.reference,
              ports: [],
              mounts: [],
              advanced: [],
            })),
            variables: [],
            warnings: [],
            unsupported: [],
            preview: "services:\n  jellyfin:\n    image: jellyfin/jellyfin:12.4\n",
          },
          selectedId: "compose-candidate",
          scannedFiles: 1,
          scannedBytes: 2048,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
    })
  })
  return { ...journey, sources: () => sources }
}

const container = (name: string) => `${name}c0ffee`.padEnd(64, "0").slice(0, 64)

/** The whole graph, in the order a pass runs it, as the manifest declares it. */
const GRAPH: [id: string, service: string, title: string, stage: SmartStackStep["stage"]][] = [
  ["storage.folders", "jellyfin", "Make the download and library folders", "initialize"],
  [
    "jellyfin.setup",
    "jellyfin",
    "Finish Jellyfin's first-run setup and create its administrator",
    "initialize",
  ],
  ["jellyfin.libraries", "jellyfin", "Create the Movies and Shows libraries", "initialize"],
  [
    "qbittorrent.auth",
    "qbittorrent",
    "Give qBittorrent's web interface its own password",
    "initialize",
  ],
  [
    "qbittorrent.categories",
    "qbittorrent",
    "Set download folders and the movies and tv categories",
    "initialize",
  ],
  ["radarr.auth", "radarr", "Secure Radarr with its own sign-in", "initialize"],
  ["radarr.rootfolder", "radarr", "Point Radarr at the Movies library", "initialize"],
  ["sonarr.auth", "sonarr", "Secure Sonarr with its own sign-in", "initialize"],
  ["sonarr.rootfolder", "sonarr", "Point Sonarr at the Shows library", "initialize"],
  ["prowlarr.auth", "prowlarr", "Secure Prowlarr with its own sign-in", "initialize"],
  ["bazarr.auth", "bazarr", "Secure Bazarr with its own sign-in", "initialize"],
  ["recyclarr.sync", "recyclarr", "Sync quality profiles into Radarr and Sonarr", "link"],
  ["radarr.downloadclient", "radarr", "Connect Radarr to qBittorrent", "link"],
  ["sonarr.downloadclient", "sonarr", "Connect Sonarr to qBittorrent", "link"],
  ["prowlarr.apps", "prowlarr", "Share Prowlarr's sources with Radarr and Sonarr", "link"],
  [
    "bazarr.link",
    "bazarr",
    "Connect Bazarr to Radarr and Sonarr in your subtitle languages",
    "link",
  ],
  ["seerr.setup", "seerr", "Sign Seerr in to Jellyfin and sync its libraries", "link"],
  ["seerr.managers", "seerr", "Connect Seerr to Radarr and Sonarr", "link"],
  ["seerr.policy", "seerr", "Apply who may request and whether requests need approval", "link"],
  [
    "downloads.verify",
    "qbittorrent",
    "Test Radarr's and Sonarr's connection to qBittorrent",
    "verify",
  ],
  ["seerr.verify", "seerr", "Test Seerr's connections", "verify"],
  ["prowlarr.sources", "prowlarr", "Check for a media source", "verify"],
]

/** Steps the Essential preset has the services for; the rest are skipped in it. */
const ESSENTIAL_STEPS = new Set([
  "storage.folders",
  "jellyfin.setup",
  "jellyfin.libraries",
  "seerr.setup",
  "seerr.policy",
  "seerr.verify",
])

/** The address an app answers at once the live release serves it: the domain, or its sibling. */
export function appUrl(service: { name: string; address?: string; primary?: boolean }) {
  if (service.primary) return "https://media.example.test"
  return service.address ? `https://media-${service.address}.example.test` : undefined
}

const SUMMARY: Record<SmartStackStatus, string> = {
  waiting: "The media server is not installed yet: its first release is still coming up.",
  configuring: "Setting up the applications: 7 of 22 steps are done.",
  ready:
    "Everything is set up and connected: a request in Seerr is searched for, downloaded and lands in Jellyfin.",
  action_required:
    "Installed and connected, but nothing can be downloaded until you add a media source.",
  failed:
    "Setup stopped at qBittorrent, so nothing can be downloaded yet. Jellyfin and Seerr are set up.",
}

/** How far through the graph the configuring state is. */
const CONFIGURING_AT = 7

/** What blocks behind the failed step in the failed state. */
const BLOCKED = new Set([
  "qbittorrent.categories",
  "radarr.downloadclient",
  "sonarr.downloadclient",
  "downloads.verify",
])

function steps(status: SmartStackStatus, essential: boolean): SmartStackStep[] {
  return GRAPH.map(([id, service, title, stage], index) => {
    const base = { id, service, title, stage, attempts: 1 }
    if (essential && !ESSENTIAL_STEPS.has(id))
      return {
        ...base,
        attempts: 0,
        status: "skipped",
        message: "Not part of the Essential preset",
      }
    if (status === "waiting") return { ...base, attempts: 0, status: "pending" }
    if (status === "configuring") {
      if (index < CONFIGURING_AT)
        return { ...base, status: "done", message: "Done", startedAt: now }
      if (index === CONFIGURING_AT) return { ...base, attempts: 2, status: "running" }
      return { ...base, attempts: 0, status: "pending" }
    }
    if (status === "failed") {
      if (id === "qbittorrent.auth")
        return {
          ...base,
          attempts: 8,
          status: "failed",
          message: "Signing in to the web interface",
          error:
            "qBittorrent answered 403 to POST /api/v2/auth/login: the temporary password in its log was not accepted ([redacted])",
        }
      if (BLOCKED.has(id))
        return {
          ...base,
          attempts: 0,
          status: "skipped",
          message: "Waiting on qBittorrent's password",
        }
    }
    if (status === "action_required" && id === "prowlarr.sources")
      return {
        ...base,
        status: "action_required",
        message: "No source has been added to Prowlarr",
      }
    return { ...base, status: "done", message: "Checked and unchanged", finishedAt: now }
  })
}

/**
 * A stack project's state in one of the server's five, for the Complete
 * preset or the Essential one. `addressless` is a stack made before every
 * app had an address: only Jellyfin answers on the domain, and the others
 * are listed as missing theirs. `noDomain` is one with no domain at all.
 */
export function stackState(
  status: SmartStackStatus,
  options: {
    essential?: boolean
    addressless?: boolean
    noDomain?: boolean
  } & Partial<SmartStackState> = {},
): SmartStackState {
  const { essential = false, addressless = false, noDomain = false, ...overrides } = options
  const waiting = status === "waiting"
  const settled = status === "ready" || status === "action_required"
  const names = essential ? ESSENTIAL : SERVICES.map((service) => service.name)
  const included = SERVICES.filter((service) => names.includes(service.name))
  const served = (service: (typeof SERVICES)[number]) =>
    !waiting && !noDomain && (!addressless || "primary" in service)
  const missing = addressless || noDomain ? included.filter((service) => "address" in service) : []
  return {
    stackId: "media",
    title: "Media server",
    version: "1.1.0",
    preset: essential ? "essential" : "complete",
    presetTitle: essential ? "Essential" : "Complete",
    status,
    summary: essential
      ? "Jellyfin and Seerr are set up and signed in to each other. Requests are collected; nothing fetches them."
      : SUMMARY[status],
    readiness: {
      installed: !waiting,
      healthy: !waiting,
      connected: settled,
      acquisition: status === "ready" && !essential,
      playback: !waiting && status !== "configuring",
    },
    services: included.map((service) => ({
      name: service.name,
      title: service.title,
      purpose: service.purpose,
      access: service.access,
      image: service.image,
      ...("docsUrl" in service ? { docsUrl: service.docsUrl } : {}),
      ...("port" in service && !waiting ? { address: `127.0.0.1:${23000 + service.port}` } : {}),
      ...(served(service) && appUrl(service) ? { url: appUrl(service) } : {}),
      ...(waiting
        ? {}
        : {
            container: {
              name: `media-server-${service.name}-1`,
              state: "running",
              ...(service.access === "worker" ? {} : { health: "healthy" }),
            },
          }),
      status: waiting
        ? "waiting"
        : status === "configuring"
          ? "configuring"
          : status === "failed" && service.name === "qbittorrent"
            ? "failed"
            : status === "action_required" && service.name === "prowlarr"
              ? "action_required"
              : "ok",
    })),
    steps: steps(status, essential),
    actions:
      status === "action_required"
        ? [
            // Listed with the optional one first, as a server might: the page
            // puts what holds the stack from ready before it.
            {
              id: "seerr-people",
              service: "seerr",
              title: "Let people in to Seerr",
              description:
                "Only the administrator can sign in so far. Import your Jellyfin users in Seerr when you want others to ask for titles.",
              blocking: false,
            },
            {
              id: "media-source",
              service: "prowlarr",
              title: "Add a media source",
              description:
                "Radarr and Sonarr search the stack's sources for what is asked for, and it has none. Choose one you are allowed to use.",
              blocking: true,
            },
          ]
        : [],
    credentials: [
      { name: "login", service: "", label: "Every app", username: "admin", stored: !waiting },
    ],
    login: { username: "admin", stored: !waiting },
    addresses: {
      primary: noDomain ? "" : "media.example.test",
      missing: missing.map((service) => ({
        service: service.name,
        title: service.title,
        hostname: noDomain
          ? ""
          : `media-${"address" in service ? service.address : ""}.example.test`,
      })),
    },
    limitations: essential ? ESSENTIAL_LIMITATIONS : COMPLETE_LIMITATIONS,
    ...(waiting ? {} : { liveReleaseId: 20 }),
    ...(settled ? { configuredReleaseId: 20 } : {}),
    ...(waiting ? {} : { startedAt: now }),
    ...(settled || status === "failed" ? { finishedAt: now } : {}),
    updatedAt: now,
    ...overrides,
  }
}

/** The project's runtime read for a stack: one container per service, by its Compose name. */
function stackRuntime(state: SmartStackState): DeploymentRuntimeServices {
  return {
    status: "available",
    observedAt: now,
    services: state.services
      .filter((service) => service.container)
      .map((service) => ({
        containerId: container(service.name),
        name: service.container!.name,
        releaseId: 20,
        liveRelease: true,
        state: service.container!.state,
        health: service.container!.health ?? "",
        imageId: `sha256:${"a".repeat(64)}`,
        image: service.image,
        stack: "media-server",
        service: service.name,
        startedAt: now,
      })),
  }
}

export const STACK_PASSWORD = "fixture-Every-app-pass-0001"

/** The domains a stack's plan carries: Jellyfin's, and each other app's routed to its service. */
export function stackDomains(state: SmartStackState | null) {
  const names = new Set(state?.services.map((service) => service.name) ?? [])
  const missing = new Set(state?.addresses?.missing.map((entry) => entry.service) ?? [])
  if (state?.addresses?.primary === "") return []
  return [
    { hostname: "media.example.test", https: true, ownership: "managed" as const },
    ...SERVICES.filter(
      (service) => "address" in service && names.has(service.name) && !missing.has(service.name),
    ).map((service) => ({
      hostname: `media-${"address" in service ? service.address : ""}.example.test`,
      https: true,
      ownership: "managed" as const,
      service: service.name,
      ...("port" in service ? { port: service.port } : {}),
    })),
  ]
}

function refuse(route: Route, status: number, code: string, message: string) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify({ error: { code, message } }),
  })
}

/**
 * Project 7 as a media stack, over `mockProject`: a Compose project made
 * from the stack's template, its stack state and domains, the pass a press
 * starts, the login's reveal and change, and "give every app its address".
 *
 * `state` may be replaced while the page is open (`set`), which is how a
 * spec watches a pass move. `configure` decides what a press is answered
 * with: 202 and a state that follows, or one of the two refusals; `login`
 * and `addresses` do the same for their routes.
 */
export async function mockStackProject(
  page: Page,
  options: {
    state?: SmartStackState | null
    showcase?: boolean
    /** What `POST …/stack/configure` answers. Default: 202, and the state becomes configuring. */
    configure?: "accepted" | "stack_busy" | "stack_waiting"
    /** What `PUT …/stack/login` answers. Default: 202, and the state becomes configuring. */
    login?: "accepted" | "password_weak"
    /** What `POST …/stack/addresses` answers. Default: 202 with run 91, and every app has its address. */
    addresses?: "accepted" | "stack_addresses_present" | "stack_no_domain"
    /** A deployment run (84) is in flight whatever the stack's state; by default only while waiting. */
    running?: boolean
  } = {},
) {
  const base = await mockProject(page, { showcase: options.showcase })
  let state: SmartStackState | null =
    options.state === undefined ? stackState("ready") : options.state
  let reads = 0
  const configures: unknown[] = []
  const reveals: string[] = []
  const logins: unknown[] = []
  const addressRequests: unknown[] = []
  const template = () =>
    state?.preset === "essential" ? mediaEssentialDefinition : mediaCompleteDefinition
  // The plan's own domains, which the Domains page reads and saves.
  base.configuration().domains = stackDomains(state)

  await page.route(
    (url) => url.pathname === "/api/v1/deploy/7",
    (route) =>
      route.request().method() !== "GET"
        ? route.fallback()
        : json(route, {
            project: { ...project, name: "media-server", profile: "compose" },
            running: state?.status !== "waiting",
            runtime: state ? stackRuntime(state) : undefined,
            deployment: {
              ...deployment,
              name: "media-server",
              profile: "compose",
              sourceKind: "compose",
              buildMethod: "compose",
              sourceTemplate: `${template().id}@1.1.0`,
              sourceRef: undefined,
              sourceRepository: undefined,
              sourceRemote: undefined,
              recipe: undefined,
              framework: undefined,
              endpoint: "https://media.example.test",
              internalPort: 8096,
              health: state?.status === "waiting" ? "unavailable" : "healthy",
              serviceCount: state?.services.length ?? 8,
              pendingChanges: false,
              liveReleaseId: state?.status === "waiting" ? undefined : 20,
              // Only a stack still waiting for its release has a run in flight,
              // unless the spec puts one there.
              activeRun: options.running || state?.status === "waiting" ? run : undefined,
              lastRun: { ...run, state: state?.status === "waiting" ? "starting" : "succeeded" },
              recentRuns: [],
            },
          }),
  )
  await page.route("**/api/v1/deploy/blueprints/media-*", (route) => json(route, template()))
  await page.route("**/api/v1/deploy/7/stack", (route) => {
    reads += 1
    if (!state) return refuse(route, 404, "stack_not_found", "This project is not a stack")
    return json(route, state)
  })
  await page.route("**/api/v1/deploy/7/stack/configure", (route) => {
    configures.push(route.request().postDataJSON())
    const answer = options.configure ?? "accepted"
    if (answer !== "accepted")
      return refuse(
        route,
        409,
        answer,
        answer === "stack_busy" ? "a pass is already running" : "no live release yet",
      )
    if (state) state = stackState("configuring", { essential: state.preset === "essential" })
    return route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ status: "configuring" }),
    })
  })
  await page.route("**/api/v1/deploy/7/stack/login", (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    logins.push(route.request().postDataJSON())
    if (options.login === "password_weak")
      return refuse(
        route,
        422,
        "password_weak",
        "The password needs at least 12 characters, mixing three of upper case, lower case, digits and symbols.",
      )
    if (state) state = stackState("configuring", { essential: state.preset === "essential" })
    return route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ status: "configuring" }),
    })
  })
  await page.route("**/api/v1/deploy/7/stack/addresses", (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    addressRequests.push(route.request().postDataJSON() ?? {})
    const answer = options.addresses ?? "accepted"
    if (answer === "stack_addresses_present")
      return refuse(route, 409, answer, "every service already has its address")
    if (answer === "stack_no_domain")
      return refuse(route, 422, answer, "the deployment has no primary domain")
    const domains = state?.addresses?.missing.map((entry) => entry.hostname) ?? []
    if (state) {
      state = stackState(state.status, { essential: state.preset === "essential" })
      base.configuration().domains = stackDomains(state)
    }
    return route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ runId: 91, domains }),
    })
  })
  await page.route("**/api/v1/deploy/7/stack/credentials/*/reveal", (route) => {
    const name = new URL(route.request().url()).pathname.split("/").at(-2) ?? ""
    reveals.push(name)
    return json(route, { name, username: "admin", password: STACK_PASSWORD })
  })
  return {
    ...base,
    set(next: SmartStackState | null) {
      state = next
    },
    reads: () => reads,
    configures: () => configures,
    reveals: () => reveals,
    logins: () => logins,
    addressRequests: () => addressRequests,
    containerId: container,
  }
}

// ---------------------------------------------------------------------------
// Media sources
// ---------------------------------------------------------------------------

/**
 * What Prowlarr can add, as the catalogue lists it: three named definitions
 * with each kind of field between them, and enough fixture ones after them
 * that the list is cut. None is a real tracker.
 */
export const SOURCE_DEFINITIONS: SmartStackSourceDefinition[] = [
  {
    definition: "communitylibrary",
    name: "Community Library",
    description: "A members' archive of community recordings",
    privacy: "semiPrivate",
    language: "en-US",
    needsInput: true,
  },
  {
    definition: "torznab",
    name: "Generic Torznab",
    description: "Any Torznab feed you run yourself",
    privacy: "private",
    language: "en-US",
    needsInput: true,
  },
  {
    definition: "publicdomainarchive",
    name: "Public Domain Archive",
    description: "Films whose copyright has expired",
    privacy: "public",
    language: "en-US",
    needsInput: false,
  },
  ...Array.from({ length: 64 }, (_, index) => {
    const number = String(index + 1).padStart(2, "0")
    return {
      definition: `sampleindex${number}`,
      name: `Sample Index ${number}`,
      description: "A fixture source with nothing to fill in",
      privacy: index % 2 === 0 ? ("public" as const) : ("private" as const),
      language: "en-US",
      needsInput: false,
    }
  }),
]

const SOURCE_FIELDS: Record<string, SmartStackSourceDetail["fields"]> = {
  torznab: [
    {
      name: "baseUrl",
      label: "URL",
      help: "The address of your Torznab feed",
      type: "text",
      value: "",
      advanced: false,
    },
    { name: "apiKey", label: "API key", type: "password", value: "", advanced: false },
    {
      name: "minimumSeeders",
      label: "Minimum seeders",
      type: "number",
      value: 1,
      advanced: true,
    },
  ],
  communitylibrary: [
    { name: "username", label: "User name", type: "text", value: "", advanced: false },
    { name: "password", label: "Password", type: "password", value: "", advanced: false },
    {
      name: "freeleech",
      label: "Only what does not count against your ratio",
      type: "checkbox",
      value: false,
      advanced: false,
    },
    {
      name: "sort",
      label: "Sort results by",
      type: "select",
      value: 0,
      options: [
        { value: 0, label: "Created" },
        { value: 1, label: "Seeders" },
      ],
      advanced: true,
    },
  ],
}

export function sourceDetail(definition: string): SmartStackSourceDetail | undefined {
  const listed = SOURCE_DEFINITIONS.find((one) => one.definition === definition)
  if (!listed) return undefined
  return {
    definition: listed.definition,
    name: listed.name,
    description: listed.description,
    privacy: listed.privacy,
    urls: [`https://${listed.definition}.example.test/`],
    fields: SOURCE_FIELDS[definition] ?? [],
  }
}

/** A typed value the fixture's Prowlarr refuses, so a spec can watch a test fail. */
export const REFUSED_KEY = "wrong-key"

/**
 * Project 7's media sources, over `mockStackProject`: the sources Prowlarr
 * holds, its catalogue and one definition's fields, Add (tested, then kept)
 * and Remove. `refusal` answers every route with one of their refusals.
 */
export async function mockStackSources(
  page: Page,
  options: {
    sources?: SmartStackSource[]
    refusal?: "stack_waiting" | "stack_no_sources" | "stack_app_unreachable"
  } = {},
) {
  let sources = [...(options.sources ?? [])]
  const added: { definition: string; fields: Record<string, unknown> }[] = []
  const removed: number[] = []
  let next = 10
  const prefix = "/api/v1/deploy/7/stack/sources"
  await page.route(
    (url) => url.pathname === prefix || url.pathname.startsWith(`${prefix}/`),
    async (route) => {
      const request = route.request()
      const path = new URL(request.url()).pathname.slice(prefix.length)
      const method = request.method()
      if (options.refusal)
        return refuse(
          route,
          options.refusal === "stack_app_unreachable" ? 502 : 409,
          options.refusal,
          "refused",
        )
      if (path === "" && method === "GET") return json(route, { sources })
      if (path === "/catalogue" && method === "GET")
        return json(route, { definitions: SOURCE_DEFINITIONS, fetchedAt: now })
      const detail = /^\/catalogue\/([^/]+)$/.exec(path)
      if (detail && method === "GET") {
        const found = sourceDetail(decodeURIComponent(detail[1]))
        return found ? json(route, found) : refuse(route, 404, "not_found", "no such definition")
      }
      if (path === "" && method === "POST") {
        const body = request.postDataJSON() as {
          definition: string
          fields: Record<string, unknown>
        }
        added.push(body)
        const listed = SOURCE_DEFINITIONS.find((one) => one.definition === body.definition)
        if (Object.values(body.fields).includes(REFUSED_KEY))
          return refuse(
            route,
            422,
            "source_test_failed",
            "Unable to connect to indexer: the feed answered 401 Unauthorized for the API key ([redacted]).",
          )
        const source: SmartStackSource = {
          id: next++,
          name: listed?.name ?? body.definition,
          definition: body.definition,
          privacy: listed?.privacy ?? "public",
          protocol: "torrent",
          enabled: true,
        }
        sources = [...sources, source]
        return route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({ source, synced: true }),
        })
      }
      const one = /^\/(\d+)$/.exec(path)
      if (one && method === "DELETE") {
        removed.push(Number(one[1]))
        sources = sources.filter((source) => source.id !== Number(one[1]))
        return route.fulfill({ status: 204 })
      }
      return route.fallback()
    },
  )
  return { sources: () => sources, added: () => added, removed: () => removed }
}

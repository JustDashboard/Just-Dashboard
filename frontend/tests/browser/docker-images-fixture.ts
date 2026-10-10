import type { Page, Route } from "@playwright/test"

/**
 * A self-hosting server's images, as /docker/images draws them: services
 * running from registry tags (two of which have moved on), the previous
 * Postgres left behind by an upgrade, a base image kept for builds, an
 * application built here, a digest-pinned proxy, a registry that would not
 * answer, a stopped workflow engine and two untagged layers a rebuild left.
 */

const now = Date.now()
const ago = (hours: number) => new Date(now - hours * 3600_000).toISOString()
const MB = 1024 * 1024

export const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: ago(1),
    createdAt: ago(900),
  },
}

const sha = (seed: string) => `sha256:${seed.repeat(64).slice(0, 64)}`

export const IMAGES = [
  {
    id: sha("a1"),
    repoTags: ["nginx:1.27-alpine"],
    repoDigests: [`nginx@${sha("e1")}`],
    size: 48 * MB,
    created: ago(24 * 41 + 9.4),
    containers: 2,
    labels: { maintainer: "NGINX Docker Maintainers" },
    dangling: false,
  },
  {
    id: sha("b2"),
    repoTags: ["postgres:16"],
    repoDigests: [`postgres@${sha("e2")}`],
    size: 432 * MB,
    created: ago(24 * 19 + 8.4),
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: sha("c3"),
    repoTags: ["postgres:15"],
    repoDigests: [`postgres@${sha("e3")}`],
    size: 412 * MB,
    created: ago(24 * 210 + 3.4),
    containers: 0,
    labels: {},
    dangling: false,
  },
  {
    id: sha("d4"),
    repoTags: ["redis:7.2-alpine"],
    repoDigests: [`redis@${sha("e4")}`],
    size: 41 * MB,
    created: ago(24 * 33 + 8.4),
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: sha("f5"),
    repoTags: ["grafana/grafana:11.2.0"],
    repoDigests: [`grafana/grafana@${sha("e5")}`],
    size: 470 * MB,
    created: ago(24 * 60 + 7.4),
    containers: 1,
    labels: {
      "org.opencontainers.image.version": "11.2.0",
      "org.opencontainers.image.source": "https://github.com/grafana/grafana",
    },
    dangling: false,
  },
  {
    id: sha("a6"),
    repoTags: ["prom/prometheus:latest"],
    repoDigests: [`prom/prometheus@${sha("e6")}`],
    size: 287 * MB,
    created: ago(24 * 52 + 6.4),
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: sha("b7"),
    repoTags: ["acme/api:dev", "acme/api:rollback"],
    repoDigests: [],
    size: 312 * MB,
    created: ago(3),
    containers: 2,
    labels: { "org.opencontainers.image.source": "https://github.com/acme/api" },
    dangling: false,
  },
  {
    id: sha("c8"),
    repoTags: ["caddy:2"],
    repoDigests: [`caddy@${sha("e8")}`],
    size: 49 * MB,
    created: ago(24 * 70 + 3.4),
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: sha("d9"),
    repoTags: ["node:22-alpine"],
    repoDigests: [`node@${sha("e9")}`],
    size: 157 * MB,
    created: ago(24 * 12 + 8.4),
    containers: 0,
    labels: {},
    dangling: false,
  },
  {
    id: sha("fa"),
    repoTags: ["mongo:7"],
    repoDigests: [`mongo@${sha("ea")}`],
    size: 712 * MB,
    created: ago(24 * 95 + 7.4),
    containers: 0,
    labels: {},
    dangling: false,
  },
  {
    id: sha("ab"),
    repoTags: ["vaultwarden/server:latest"],
    repoDigests: [`vaultwarden/server@${sha("eb")}`],
    size: 260 * MB,
    created: ago(24 * 28 + 3.4),
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: sha("bc"),
    repoTags: ["n8nio/n8n:1.64.3"],
    repoDigests: [`n8nio/n8n@${sha("ec")}`],
    size: 1130 * MB,
    created: ago(24 * 45 + 6.4),
    containers: 1,
    labels: { "org.opencontainers.image.version": "1.64.3" },
    dangling: false,
  },
  {
    id: sha("cd"),
    repoTags: [],
    repoDigests: [],
    size: 245 * MB,
    created: ago(26),
    containers: 0,
    labels: {},
    dangling: true,
  },
  {
    id: sha("de"),
    repoTags: [],
    repoDigests: [],
    size: 88 * MB,
    created: ago(24 * 6 + 9.4),
    containers: 0,
    labels: {},
    dangling: true,
  },
]

/** The image a fresh pull lands as, for the arrival a pull ends on. */
export const PULLED = {
  id: sha("ef"),
  repoTags: ["valkey/valkey:8"],
  repoDigests: [`valkey/valkey@${sha("ee")}`],
  size: 98 * MB,
  created: ago(24 * 2 + 5.4),
  containers: 0,
  labels: {},
  dangling: false,
}

function container(
  id: string,
  name: string,
  image: string,
  imageId: string,
  state: "running" | "exited" | "restarting",
  composeStack?: string,
) {
  return {
    id: id.repeat(16).slice(0, 16),
    names: [`/${name}`],
    name,
    image,
    imageId,
    command: "",
    state,
    status: state === "running" ? "Up 3 days" : "Exited (0) 2 days ago",
    createdAt: ago(24 * 9),
    startedAt: ago(72),
    uptimeSeconds: state === "running" ? 72 * 3600 : 0,
    ports: [],
    labels: {},
    networks: ["bridge"],
    exposure: [],
    hasHealthcheck: false,
    inspected: state === "running",
    composeStack,
  }
}

export const CONTAINERS = [
  container("1", "web", "nginx:1.27-alpine", sha("a1"), "running", "site"),
  container("2", "docs", "nginx:1.27-alpine", sha("a1"), "running", "site"),
  container("3", "db", "postgres:16", sha("b2"), "running", "site"),
  container("4", "cache", "redis:7.2-alpine", sha("d4"), "running", "site"),
  container("5", "grafana", "grafana/grafana:11.2.0", sha("f5"), "running"),
  container("6", "prometheus", "prom/prometheus:latest", sha("a6"), "running"),
  container("7", "api", "acme/api:dev", sha("b7"), "running", "acme"),
  container("8", "worker", "acme/api:dev", sha("b7"), "restarting", "acme"),
  container("9", "proxy", `caddy@${sha("e8")}`, sha("c8"), "running"),
  container("a", "vaultwarden", "vaultwarden/server:latest", sha("ab"), "running"),
  container("b", "n8n", "n8nio/n8n:1.64.3", sha("bc"), "exited"),
]

const checked = ago(0.2)

export const UPDATES = {
  "nginx:1.27-alpine": {
    ref: "nginx:1.27-alpine",
    state: "outdated",
    localDigest: sha("e1"),
    remoteDigest: sha("f1"),
    checkedAt: checked,
  },
  "postgres:16": { ref: "postgres:16", state: "current", checkedAt: checked },
  "redis:7.2-alpine": { ref: "redis:7.2-alpine", state: "current", checkedAt: checked },
  "grafana/grafana:11.2.0": { ref: "grafana/grafana:11.2.0", state: "current", checkedAt: checked },
  "prom/prometheus:latest": {
    ref: "prom/prometheus:latest",
    state: "outdated",
    localDigest: sha("e6"),
    remoteDigest: sha("f6"),
    checkedAt: checked,
  },
  "acme/api:dev": {
    ref: "acme/api:dev",
    state: "local",
    reason: "Built on this server and never pushed, so no registry holds a copy to compare.",
    checkedAt: checked,
  },
  "caddy:2": {
    ref: `caddy@${sha("e8")}`,
    state: "pinned",
    reason: "The container names an exact digest, which cannot point anywhere else.",
    checkedAt: checked,
  },
  "vaultwarden/server:latest": {
    ref: "vaultwarden/server:latest",
    state: "unknown",
    reason: "Docker Hub answered 429: too many requests from this address. Try again later.",
    checkedAt: checked,
  },
  "n8nio/n8n:1.64.3": { ref: "n8nio/n8n:1.64.3", state: "current", checkedAt: checked },
}

const imagesSize = IMAGES.reduce((sum, image) => sum + image.size, 0)
const shared = 610 * MB

export const DISK = {
  layersSize: imagesSize - shared,
  imagesSize,
  containersSize: 37 * MB,
  volumesSize: 2.8 * 1024 * MB,
  buildCacheSize: 1.9 * 1024 * MB,
  sharedLayers: shared,
  writable: [],
  definitions: [
    {
      key: "images",
      label: "Image layers",
      measures: "The bytes every image layer occupies on disk, each shared layer counted once.",
      excludes: "Container writes, volumes and the build cache.",
      source: "GET /system/df",
    },
  ],
  images: {
    total: IMAGES.length,
    active: 9,
    size: imagesSize - shared,
    reclaimable: 1.6 * 1024 * MB,
  },
  containers: { total: 11, active: 9, size: 37 * MB, reclaimable: 6 * MB },
  volumes: { total: 7, active: 5, size: 2.8 * 1024 * MB, reclaimable: 310 * MB },
  buildCache: { total: 48, active: 0, size: 1.9 * 1024 * MB, reclaimable: 1.9 * 1024 * MB },
}

function layers(image: (typeof IMAGES)[number]) {
  const base = image.repoTags[0]?.includes("alpine")
    ? { size: 7.8 * MB, createdBy: "/bin/sh -c #(nop) ADD file:9a4f77dfaba7fd2aa in / " }
    : { size: 74.8 * MB, createdBy: "/bin/sh -c #(nop) ADD file:bc7e1f1a4f8b3d2c in / " }
  const rest = Math.max(image.size - base.size, 0)
  return [
    { createdBy: 'CMD ["nginx" "-g" "daemon off;"]', size: 0 },
    { createdBy: "STOPSIGNAL SIGQUIT", size: 0 },
    { createdBy: "EXPOSE map[80/tcp:{}]", size: 0 },
    { createdBy: "COPY 30-tune-worker-processes.sh /docker-entrypoint.d # buildkit", size: 4_620 },
    { createdBy: "COPY docker-entrypoint.sh / # buildkit", size: 1_620 },
    {
      createdBy:
        'RUN /bin/sh -c set -x && apkArch="$(cat /etc/apk/arch)" && apk add --no-cache --virtual .build-deps gcc libc-dev make openssl-dev pcre2-dev zlib-dev linux-headers',
      size: rest * 0.72,
    },
    {
      createdBy: "RUN /bin/sh -c apk add --no-cache curl ca-certificates tzdata",
      size: rest * 0.28,
    },
    { createdBy: "ENV NGINX_VERSION=1.27.2", size: 0 },
    base,
  ].map((layer, index) => ({
    id: index === 0 ? image.id : "<missing>",
    created: image.created,
    tags: index === 0 ? image.repoTags : [],
    ...layer,
  }))
}

export function imageDetail(id: string) {
  const image = IMAGES.find((i) => i.id === id || i.id.startsWith(id)) ?? IMAGES[0]
  const tag = image.repoTags[0]
  const usedBy = CONTAINERS.filter((c) => c.imageId === image.id).map((c) => ({
    id: c.id,
    name: c.name,
    state: c.state,
    stack: c.composeStack,
  }))
  return {
    ...image,
    architecture: "amd64",
    os: "linux",
    entrypoint: ["/docker-entrypoint.sh"],
    command: ["nginx", "-g", "daemon off;"],
    workingDir: "",
    user: "",
    exposedPorts: ["80/tcp"],
    volumePaths: image.repoTags[0]?.startsWith("postgres") ? ["/var/lib/postgresql/data"] : [],
    env: [
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      "NGINX_VERSION=1.27.2",
      "NJS_VERSION=0.8.7",
    ],
    layers: layers(image),
    usedBy,
    kind: image.dangling ? "dangling" : "tag",
    ref: tag ?? image.id.replace("sha256:", "").slice(0, 12),
    pullable: Boolean(tag) && image.repoDigests.length > 0,
    checkable: Boolean(tag) && image.repoDigests.length > 0,
    movingTag: Boolean(tag && /:(latest|\d+|\d+\.\d+(-alpine)?)$/.test(tag)),
    dangling: image.dangling,
    localBuild: !image.dangling && image.repoDigests.length === 0,
  }
}

/** The pull a test drives: three layers, one already here, the rest downloading. */
export const PULL_FRAMES = [
  { status: "Pulling from valkey/valkey", id: "8" },
  { status: "Pulling fs layer", id: "302e3ee49805" },
  { status: "Already exists", id: "a2318d6c47ec" },
  { status: "Pulling fs layer", id: "5f2c4b1c2a7d" },
  {
    status: "Downloading",
    id: "302e3ee49805",
    progress: "[=========>                                         ]  6.29MB/31.4MB",
  },
  {
    status: "Downloading",
    id: "5f2c4b1c2a7d",
    progress: "[====================================>              ]  41.9MB/57.6MB",
  },
]

export type ImageMocks = {
  /** Every mutation the page sent, as `METHOD path?query body`. */
  calls: string[]
  /** How many times the page opened the pull socket — each opening is a pull. */
  pulls: number
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export async function mockImages(
  page: Page,
  options: { images?: typeof IMAGES; capabilities?: string[]; pull?: "hold" | "finish" } = {},
): Promise<ImageMocks> {
  const mocks: ImageMocks = { calls: [], pulls: 0 }
  let images = options.images ?? IMAGES
  const session = options.capabilities ? { ...user, capabilities: options.capabilities } : user

  await page.routeWebSocket(/\/api\/v1\/docker\/images\/pull/, (ws) => {
    mocks.pulls += 1
    for (const frame of PULL_FRAMES) ws.send(JSON.stringify({ type: "progress", data: frame }))
    if (options.pull === "finish") {
      images = [PULLED, ...images.filter((i) => i.id !== PULLED.id)]
      ws.send(JSON.stringify({ type: "done", data: { ref: "valkey/valkey:8" } }))
      // The server ends the socket once the pull is over.
      void ws.close()
    }
  })

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method !== "GET") {
      mocks.calls.push(`${method} ${path}${url.search} ${request.postData() ?? ""}`.trim())
    }
    switch (path) {
      case "/auth/session":
        return json(route, session)
      case "/dashboard/update":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      case "/docker/ping":
        return json(route, { available: true, serverVersion: "29.8.0" })
      case "/docker/containers/":
        return json(route, CONTAINERS)
      case "/docker/images/":
        return json(route, images)
      case "/docker/images/updates":
        return json(route, UPDATES)
      case "/docker/disk-usage":
        return json(route, DISK)
      case "/docker/images/prune":
        return json(route, { kind: "images", spaceReclaimed: 333 * MB, items: ["postgres:15"] })
      case "/docker/build-cache/prune":
        return json(route, { kind: "build cache", spaceReclaimed: 1900 * MB, items: ["x"] })
      case "/docker/containers/prune":
        return json(route, { kind: "containers", spaceReclaimed: 6 * MB, items: ["n8n"] })
      case "/docker/prune":
        return json(route, [{ kind: "images", spaceReclaimed: 333 * MB, items: ["postgres:15"] }])
    }
    const tag = /^\/docker\/images\/([^/]+)\/tag$/.exec(path)
    if (tag && method === "POST") return route.fulfill({ status: 204 })
    const one = /^\/docker\/images\/([^/]+)$/.exec(path)
    if (one && method === "DELETE") {
      const id = decodeURIComponent(one[1])
      images = images.filter((i) => i.id !== id)
      return json(route, [{ deleted: id }])
    }
    if (one) return json(route, imageDetail(decodeURIComponent(one[1])))
    // Anything not named is answered as the Docker suite answers it: a clean
    // "not mocked" rather than an empty array the shell would misread.
    return json(route, { error: { code: "not_available", message: "Not mocked" } }, 503)
  })
  return mocks
}

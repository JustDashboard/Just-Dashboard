import type { Page, Route } from "@playwright/test"

const now = new Date().toISOString()
const twoDaysAgo = new Date(Date.now() - 2 * 24 * 3600_000).toISOString()

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
    lastLoginAt: now,
    createdAt: now,
  },
}

export const installed = [
  {
    name: "nginx",
    version: "1.24.0-2ubuntu7",
    summary: "small, powerful, scalable web/proxy server",
    size: 1_540_000,
    section: "httpd",
    explicit: true,
    upgradable: "1.24.0-2ubuntu7.1",
  },
  {
    name: "openssl",
    version: "3.0.13-0ubuntu3",
    summary: "Secure Sockets Layer toolkit - cryptographic utility",
    size: 2_030_000,
    section: "utils",
    explicit: false,
    essential: true,
    upgradable: "3.0.13-0ubuntu3.4",
    security: true,
  },
  {
    name: "postgresql-16",
    version: "16.3-0ubuntu0.24.04.1",
    summary: "The World's Most Advanced Open Source Relational Database",
    size: 48_000_000,
    section: "database",
    explicit: true,
  },
  {
    name: "curl",
    version: "8.5.0-2ubuntu10.1",
    summary: "command line tool for transferring data with URL syntax",
    size: 530_000,
    section: "web",
    explicit: true,
    upgradable: "8.5.0-2ubuntu10.4",
  },
  {
    name: "libc6",
    version: "2.39-0ubuntu8",
    summary: "GNU C Library: Shared libraries",
    size: 13_000_000,
    section: "libs",
    explicit: false,
    essential: true,
  },
  {
    name: "gcc-13",
    version: "13.2.0-23ubuntu4",
    summary: "GNU C compiler",
    size: 96_000_000,
    section: "devel",
    explicit: false,
  },
  {
    name: "linux-image-6.8.0-45-generic",
    version: "6.8.0-45.45",
    summary: "Linux kernel image for the host",
    section: "kernel",
    size: 480_000_000,
    explicit: true,
    essential: true,
  },
  {
    name: "docker.io",
    version: "26.1.3-0ubuntu1",
    summary: "Linux container runtime",
    section: "admin",
    size: 260_000_000,
    explicit: true,
  },
  {
    name: "containerd",
    version: "1.7.24",
    summary: "Container runtime daemon",
    section: "admin",
    size: 110_000_000,
    explicit: false,
  },
  {
    name: "python3.12",
    version: "3.12.3-1ubuntu0.2",
    summary: "Interactive high-level programming language",
    section: "python",
    size: 115_000_000,
    explicit: true,
  },
  {
    name: "libpython3.12-stdlib",
    version: "3.12.3-1ubuntu0.2",
    summary: "Python standard library",
    section: "libs",
    size: 32_000_000,
    explicit: false,
  },
  {
    name: "nodejs",
    version: "20.18.0",
    summary: "JavaScript runtime built on V8",
    section: "javascript",
    size: 90_000_000,
    explicit: true,
  },
  {
    name: "git",
    version: "2.43.0-1ubuntu7.1",
    summary: "Fast, scalable, distributed revision control system",
    section: "vcs",
    size: 35_000_000,
    explicit: true,
  },
  {
    name: "redis-server",
    version: "7.0.15-1build2",
    summary: "Persistent key-value database",
    section: "database",
    size: 9_000_000,
    explicit: true,
  },
]

export const inventory = {
  available: true,
  manager: "apt",
  packages: installed,
  explicitCount: installed.filter((p) => p.explicit).length,
  totalSize: installed.reduce((total, p) => total + p.size, 0),
  upgradeCount: 3,
  securityCount: 1,
  canInstall: true,
  canPurge: true,
  indexAge: twoDaysAgo,
  canRefresh: true,
  readAt: now,
}

export const report = {
  available: true,
  manager: "apt",
  packages: [
    {
      name: "openssl",
      current: "3.0.13-0ubuntu3",
      candidate: "3.0.13-0ubuntu3.4",
      origin: "Ubuntu:24.04/noble-security",
      security: true,
    },
    {
      name: "nginx",
      current: "1.24.0-2ubuntu7",
      candidate: "1.24.0-2ubuntu7.1",
      origin: "Ubuntu:24.04/noble-updates",
      security: false,
    },
    {
      name: "curl",
      current: "8.5.0-2ubuntu10.1",
      candidate: "8.5.0-2ubuntu10.4",
      origin: "Ubuntu:24.04/noble-updates",
      security: false,
    },
  ],
  securityCount: 1,
  securityFiltering: true,
  rebootRequired: false,
  lastChecked: now,
}

const host = {
  hostname: "web-1",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0-45-generic",
  kernelArch: "x86_64",
  virtualization: "kvm",
  bootTime: now,
  uptimeSeconds: 86400,
  processes: 212,
  cpuModel: "AMD EPYC 7B13",
  cpuCores: 4,
  cpuMhz: 2450,
}

const search = [
  {
    name: "htop",
    version: "3.3.0-4build1",
    summary: "interactive processes viewer",
    repository: "universe",
    installed: false,
  },
  {
    name: "btop",
    version: "1.3.0-1",
    summary: "Modern and colorful command line resource monitor",
    repository: "universe",
    installed: true,
    installedVersion: "1.3.0-1",
  },
]

const detail = (name: string) => {
  const row = installed.find((p) => p.name === name)
  return {
    name,
    version: row?.upgradable ?? row?.version ?? "3.3.0-4build1",
    installedVersion: row?.version,
    installed: Boolean(row),
    summary: row?.summary ?? "interactive processes viewer",
    description:
      row?.summary ??
      "A cross-platform interactive process viewer.\nIt lets you see what is running.",
    homepage: `https://example.org/${name}`,
    license: "GPL-2.0",
    section: row?.section ?? "utils",
    repository: "universe",
    maintainer: "Ubuntu Developers",
    arch: "amd64",
    size: row?.size ?? 400_000,
    dependencies: ["libc6", "libncursesw6", "libtinfo6"],
    essential: row?.essential,
    upgradable: row?.upgradable,
  }
}

const usage = (name: string) => ({
  package: name,
  commands: [name],
  services: name === "nginx" ? ["nginx.service"] : undefined,
  configFiles: name === "nginx" ? ["/etc/nginx/nginx.conf"] : undefined,
  manPages: [{ name, section: "8", path: `/usr/share/man/man8/${name}.8.gz` }],
  manual: `${name.toUpperCase()}(8)\n\nNAME\n       ${name} - a program\n`,
  manualFor: name,
  empty: false,
})

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

export async function mockHost(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/system/host") return json(route, host)
    if (path === "/packages/") return json(route, inventory)
    if (path === "/packages/updates") return json(route, report)
    if (path === "/packages/search") return json(route, search)
    if (path === "/packages/install" && method === "POST") {
      return json(route, {
        id: "job-1",
        kind: "packages.install",
        title: "Install htop",
        target: "htop",
        status: "running",
        exitCode: 0,
        startedAt: now,
        lines: 0,
      })
    }
    const usageMatch = /^\/packages\/([^/]+)\/usage$/.exec(path)
    if (usageMatch) return json(route, usage(decodeURIComponent(usageMatch[1])))
    const detailMatch = /^\/packages\/([^/]+)$/.exec(path)
    if (detailMatch) return json(route, detail(decodeURIComponent(detailMatch[1])))
    if (method !== "GET") return json(route, { exitCode: 0 })
    return json(route, [])
  })
}

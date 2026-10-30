import type { Page, Route } from "@playwright/test"

const NOW = Date.now()
const HOUR = 3_600_000
const DAY = 24 * HOUR
// A few minutes past each figure, so a screenshot reads the way a real hour does.
const ago = (ms: number) => new Date(NOW - ms - 13 * 60_000).toISOString()

export const session = {
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
    lastLoginAt: ago(0),
    createdAt: ago(0),
  },
}

const closed = (why: string) => ({ allowed: false, why })
const open = { allowed: true }

const account = (fields: Record<string, unknown> & { username: string }) => ({
  gid: 1000,
  comment: "",
  home: `/home/${fields.username}`,
  shell: "/bin/bash",
  groups: [],
  primaryGroup: fields.username,
  system: false,
  locked: false,
  noPassword: false,
  sshKeyCount: 0,
  canLogin: true,
  password: "none",
  disabled: false,
  aging: { mustChange: false },
  signIn: { password: closed("no_password"), key: closed("no_keys") },
  rootRoutes: [],
  protected: false,
  ...fields,
})

const cloudInit = "/etc/sudoers.d/90-cloud-init-users"
const sudoFile = "/etc/sudoers"

const daemon = (username: string, uid: number, home = "/nonexistent") =>
  account({
    username,
    uid,
    gid: uid,
    home,
    shell: "/usr/sbin/nologin",
    groups: [username],
    system: true,
    locked: true,
    canLogin: false,
    signIn: { password: closed("no_shell"), key: closed("no_shell") },
  })

/**
 * Every state a host's account can be in that the pages say something different
 * about, so one fixture drives the specs and the screenshots alike. `root`,
 * `ubuntu` and `zoro` are the three accounts of the server this page was rebuilt
 * against, as they really stood.
 */
export const people = [
  account({
    username: "root",
    uid: 0,
    gid: 0,
    home: "/root",
    comment: "root",
    groups: ["root"],
    primaryGroup: "root",
    system: true,
    protected: true,
    password: "set",
    lastLogin: ago(7 * DAY),
    lastLoginFrom: "198.51.100.23",
    aging: { mustChange: false, lastChanged: ago(7 * DAY).slice(0, 10) },
    signIn: { password: open, key: closed("no_keys") },
    rootRoutes: [{ kind: "uid0", usable: true }],
  }),
  account({
    username: "ion",
    uid: 1002,
    comment: "Ion Moisei",
    groups: ["ion", "sudo", "docker", "adm"],
    password: "set",
    sshKeyCount: 4,
    lastLogin: ago(2 * HOUR),
    lastLoginFrom: "100.101.4.12",
    aging: { mustChange: false, lastChanged: ago(30 * DAY).slice(0, 10), maxDays: 180 },
    signIn: { password: open, key: open },
    rootRoutes: [
      { kind: "sudo", via: "sudo", ruleId: "sudo-group", file: sudoFile, usable: true },
      { kind: "group", via: "docker", usable: true },
    ],
  }),
  account({
    username: "deploy",
    uid: 1003,
    comment: "Deploy bot",
    groups: ["deploy", "docker"],
    sshKeyCount: 1,
    locked: true,
    password: "none",
    lastLogin: ago(3 * DAY),
    lastLoginFrom: "203.0.113.9",
    signIn: { password: closed("no_password"), key: open },
    rootRoutes: [
      {
        kind: "sudo",
        ruleId: "deploy-managed",
        file: "/etc/sudoers.d/jd-deploy",
        limited: true,
        noPassword: true,
        usable: true,
      },
      { kind: "group", via: "docker", usable: true },
    ],
  }),
  account({
    username: "ubuntu",
    uid: 1000,
    comment: "Ubuntu",
    groups: ["ubuntu", "adm", "cdrom", "sudo", "dip", "lxd"],
    locked: true,
    rootRoutes: [
      { kind: "sudo", ruleId: "cloud-init", file: cloudInit, noPassword: true, usable: true },
      { kind: "sudo", via: "sudo", ruleId: "sudo-group", file: sudoFile, usable: false },
      { kind: "group", via: "lxd", usable: true },
    ],
  }),
  account({
    username: "zoro",
    uid: 1001,
    groups: ["zoro", "sudo"],
    locked: true,
    rootRoutes: [
      { kind: "sudo", via: "sudo", ruleId: "sudo-group", file: sudoFile, usable: false },
    ],
  }),
  account({
    username: "mira",
    uid: 1004,
    comment: "Mira Chen",
    groups: ["mira"],
    password: "empty",
    noPassword: true,
    lastLogin: ago(40 * DAY),
    lastLoginFrom: "198.51.100.7",
    signIn: { password: open, key: closed("no_keys") },
  }),
  account({
    username: "alex",
    uid: 1005,
    comment: "Alex Doe",
    groups: ["alex", "www-data"],
    password: "locked",
    locked: true,
    disabled: true,
    sshKeyCount: 1,
    expiresOn: "1970-01-02",
    lastLogin: ago(90 * DAY),
    signIn: { password: closed("disabled"), key: closed("disabled") },
  }),
]

export const daemons = [
  daemon("postgres", 118, "/var/lib/postgresql"),
  daemon("redis", 119, "/var/lib/redis"),
  daemon("caddy", 997, "/var/lib/caddy"),
  daemon("grafana", 472, "/usr/share/grafana"),
  daemon("_apt", 42),
  daemon("www-data", 33, "/var/www"),
  daemon("daemon", 1),
  daemon("nobody", 65534),
]

const everyone = [...people, ...daemons]

const key = (fields: Record<string, unknown>) => ({
  line: 1,
  type: "ssh-ed25519",
  raw: `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA ${fields.comment}`,
  ...fields,
})

export const keysOf: Record<string, unknown[]> = {
  ion: [
    key({
      comment: "github:ion-dev",
      fingerprint: "SHA256:lQ8Hn1vDk2rS7mPq0Yt5wZxC4uE9aB3fGhJkLm0Op2c",
      lastUsed: ago(2 * HOUR),
      lastUsedFrom: "100.101.4.12",
    }),
    key({
      comment: "ion@macbook",
      fingerprint: "SHA256:9fV2bN6xQw1EoRtY5uI8oP3aSdF7gH0jKlZ4cXvB6nM",
      options: 'from="100.64.0.0/10"',
      lastUsed: ago(9 * DAY),
      lastUsedFrom: "100.101.4.12",
    }),
    key({
      comment: "github-actions deploy",
      fingerprint: "SHA256:Zx0CvB9nM8aSdF7gH6jKl5QwE4rT3yU2iO1pLk3Jh2g",
      options: 'restrict,command="/usr/local/bin/deploy",expiry-time="20270101"',
    }),
    key({
      comment: "old laptop",
      type: "ssh-rsa",
      bits: 1024,
      weak: "a 1024-bit RSA key is too short to be safe",
      fingerprint: "SHA256:Qw1EoRtY5uI8oP3aSdF7gH0jKl4cXvB6nMZx9fV2bN6",
    }),
  ],
  deploy: [
    key({
      comment: "github-actions",
      fingerprint: "SHA256:Hg5TyU7iO9pL1kJ3hG5fD7sA9qW1eR3tY5uI7oP9aS1",
      options: 'restrict,from="140.82.112.0/20"',
      lastUsed: ago(3 * DAY),
      lastUsedFrom: "203.0.113.9",
    }),
  ],
  alex: [
    key({
      comment: "alex@work",
      fingerprint: "SHA256:Aa1Bb2Cc3Dd4Ee5Ff6Gg7Hh8Ii9Jj0Kk1Ll2Mm3Nn4O",
    }),
  ],
}

const rule = (fields: Record<string, unknown>) => ({
  line: 1,
  runAs: "ALL : ALL",
  commands: ["ALL"],
  noPassword: false,
  full: true,
  asRoot: true,
  managed: false,
  ...fields,
})

const sudoGroupRule = rule({
  id: "sudo-group",
  file: sudoFile,
  line: 50,
  text: "%sudo ALL=(ALL:ALL) ALL",
  who: ["%sudo"],
})

export const sudoOf: Record<string, object> = {
  ion: { rules: [sudoGroupRule], managed: { level: "none", commands: [] } },
  zoro: { rules: [sudoGroupRule], managed: { level: "none", commands: [] } },
  ubuntu: {
    rules: [
      rule({
        id: "cloud-init",
        file: cloudInit,
        text: "ubuntu ALL=(ALL) NOPASSWD:ALL",
        who: ["ubuntu"],
        runAs: "ALL",
        noPassword: true,
      }),
      sudoGroupRule,
    ],
    managed: { level: "none", commands: [] },
  },
  deploy: {
    rules: [
      rule({
        id: "deploy-managed",
        file: "/etc/sudoers.d/jd-deploy",
        text: "deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart api",
        who: ["deploy"],
        runAs: "root",
        commands: ["/usr/bin/systemctl restart api"],
        noPassword: true,
        full: false,
        managed: true,
      }),
    ],
    managed: { level: "nopassword", commands: ["/usr/bin/systemctl restart api"] },
  },
}

export const groups = [
  {
    name: "sudo",
    gid: 27,
    members: ["ion", "ubuntu", "zoro"],
    primary: [],
    system: true,
    rootEquivalent: false,
    sudoRules: [sudoGroupRule],
    protected: true,
  },
  {
    name: "docker",
    gid: 988,
    members: ["deploy", "ion"],
    primary: [],
    system: true,
    rootEquivalent: true,
    sudoRules: [],
    protected: false,
  },
  {
    name: "lxd",
    gid: 110,
    members: ["ubuntu"],
    primary: [],
    system: true,
    rootEquivalent: true,
    sudoRules: [],
    protected: false,
  },
  {
    name: "developers",
    gid: 1100,
    members: ["alex", "ion", "mira"],
    primary: [],
    system: false,
    rootEquivalent: false,
    sudoRules: [],
    protected: false,
  },
  {
    name: "adm",
    gid: 4,
    members: ["ion", "ubuntu"],
    primary: [],
    system: true,
    rootEquivalent: false,
    sudoRules: [],
    protected: true,
  },
  {
    name: "www-data",
    gid: 33,
    members: ["alex"],
    primary: ["www-data"],
    system: true,
    rootEquivalent: false,
    sudoRules: [],
    protected: false,
  },
  ...["ion", "deploy", "ubuntu", "zoro", "mira", "alex"].map((name, index) => ({
    name,
    gid: 1000 + index,
    members: [],
    primary: [name],
    system: false,
    rootEquivalent: false,
    sudoRules: [],
    protected: false,
  })),
]

export const defaults = {
  passwordMaxDays: -1,
  passwordMinDays: 0,
  passwordWarnDays: 7,
  shell: "/bin/bash",
  inactiveDays: -1,
  quality: { available: true, minLength: 8, minClasses: 0 },
  skel: [
    { name: ".bash_logout", bytes: 220, dir: false },
    { name: ".bashrc", bytes: 3771, dir: false },
    { name: ".profile", bytes: 807, dir: false },
  ],
  shells: ["/bin/bash", "/bin/sh", "/usr/bin/zsh", "/usr/bin/fish"],
}

export const templates = [
  {
    id: "person",
    name: "Person",
    builtin: true,
    shell: "/bin/bash",
    groups: [],
    sudo: "none",
    system: false,
    noHome: false,
  },
  {
    id: "administrator",
    name: "Administrator",
    builtin: true,
    shell: "/bin/bash",
    groups: [],
    sudo: "password",
    system: false,
    noHome: false,
  },
  {
    id: "developer",
    name: "Developer",
    builtin: true,
    shell: "/bin/bash",
    groups: ["docker"],
    sudo: "none",
    system: false,
    noHome: false,
  },
  {
    id: "deploy",
    name: "Deploy bot",
    builtin: true,
    shell: "/bin/bash",
    groups: [],
    sudo: "none",
    system: false,
    noHome: false,
  },
  {
    id: "service",
    name: "Service",
    builtin: true,
    shell: "/usr/sbin/nologin",
    groups: [],
    sudo: "none",
    system: true,
    noHome: true,
  },
]

const history: Record<string, object> = {
  ion: {
    logins: [
      {
        kind: "login",
        user: "ion",
        tty: "pts/1",
        from: "100.101.4.12",
        loginTime: ago(2 * HOUR),
        active: true,
      },
      {
        kind: "login",
        user: "ion",
        tty: "pts/0",
        from: "100.101.4.12",
        loginTime: ago(DAY),
        active: false,
        duration: "01:12",
      },
      {
        kind: "login",
        user: "ion",
        tty: "pts/0",
        from: "198.51.100.23",
        loginTime: ago(4 * DAY),
        active: false,
        duration: "00:20",
      },
    ],
    failed: [
      {
        kind: "login",
        user: "ion",
        tty: "ssh:notty",
        from: "185.220.101.4",
        loginTime: ago(6 * HOUR),
        active: false,
      },
    ],
    sudo: [
      {
        at: ago(3 * HOUR),
        runAs: "root",
        command: "/usr/bin/systemctl restart caddy",
        failed: false,
      },
      { at: ago(5 * HOUR), runAs: "root", command: "/usr/bin/apt upgrade", failed: false },
      {
        at: ago(2 * DAY),
        runAs: "root",
        command: "/usr/bin/cat /etc/shadow",
        failed: true,
        reason: "3 incorrect password attempts",
      },
    ],
  },
}

const audit = {
  entries: [
    {
      id: 1,
      ts: ago(30 * HOUR),
      username: "operator",
      action: "system.user.sshkey.import",
      target: "ion",
      success: true,
      detail: "",
    },
    {
      id: 2,
      ts: ago(50 * HOUR),
      username: "operator",
      action: "system.user.password.set",
      target: "ion",
      success: true,
      detail: "",
    },
    {
      id: 3,
      ts: ago(72 * HOUR),
      username: "operator",
      action: "system.user.create",
      target: "zoro",
      success: true,
      detail: "",
    },
  ],
  total: 3,
}

const removals: Record<string, object> = {
  ion: {
    username: "ion",
    home: "/home/ion",
    homeExists: true,
    homeBytes: 3_400_000_000,
    homeUnsafe: false,
    processes: 12,
    cronJobs: true,
    sshKeys: 4,
    sudoRules: [],
    elsewhere: ["/srv/shop/uploads", "/var/tmp/ion-export.tar", "/opt/tools/ion.conf"],
    elsewhereTruncated: false,
  },
  root: {
    username: "root",
    home: "/root",
    homeExists: true,
    homeBytes: 0,
    homeUnsafe: true,
    processes: 0,
    cronJobs: false,
    sshKeys: 0,
    sudoRules: [],
    elsewhere: [],
    elsewhereTruncated: false,
    blocked: "the system depends on this account",
  },
}

const activity: Record<string, object> = {
  ion: {
    sessions: [
      {
        user: "ion",
        tty: "pts/1",
        from: "100.101.4.12",
        loginTime: ago(2 * HOUR),
        idle: "3m",
        pid: 41220,
        isSsh: true,
      },
      {
        user: "ion",
        tty: "pts/4",
        from: "198.51.100.23",
        loginTime: ago(25 * 60_000),
        pid: 41877,
        isSsh: true,
      },
    ],
    processes: 12,
    busiest: [
      { pid: 41300, cpu: 12.5, memory: 3.1, seconds: 3600, name: "node" },
      { pid: 41301, cpu: 4.5, memory: 1.1, seconds: 3600, name: "nginx" },
      { pid: 41302, cpu: 1.1, memory: 0.4, seconds: 3600, name: "bash" },
    ],
    cronJobs: 2,
  },
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Answers this section's reads and writes, and says whether it did, so a spec with
 * a mock of its own can hand these routes here.
 */
export async function answerSystemUsers(route: Route): Promise<boolean> {
  const request = route.request()
  const url = new URL(request.url())
  const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "")
  const method = request.method()
  if (path === "/system-users") {
    if (method === "POST") {
      const body = request.postDataJSON()
      await json(route, account({ username: body.username, uid: 1200 }), 201)
      return true
    }
    const system = url.searchParams.get("system") === "true"
    await json(route, system ? everyone : people)
    return true
  }
  if (path === "/system-accounts/groups") return json(route, groups).then(() => true)
  if (path === "/system-accounts/defaults") return json(route, defaults).then(() => true)
  if (path === "/system-accounts/templates") return json(route, templates).then(() => true)
  if (path === "/system-accounts/bulk" || path === "/system-accounts/bulk/delete") {
    const { usernames } = request.postDataJSON()
    await json(route, { results: usernames.map((username: string) => ({ username })) })
    return true
  }
  const match = path.match(/^\/system-users\/([^/]+)(?:\/(.+))?$/)
  if (!match) return false
  const name = decodeURIComponent(match[1])
  const rest = match[2]
  const user = everyone.find((u) => u.username === name)
  const answer = async (body: unknown, status = 200) => {
    await json(route, body, status)
    return true
  }
  if (!user) return answer({ error: "not found" }, 404)
  if (method === "POST" && rest === "keys/generate") {
    return answer(
      {
        key: key({ comment: "deploy", fingerprint: "SHA256:new" }),
        privateKey:
          "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----\n",
      },
      201,
    )
  }
  if (method !== "GET") return answer(user)
  if (!rest) {
    return answer({
      ...user,
      failedAttempts: { available: true, count: name === "ion" ? 2 : 0 },
      shells: defaults.shells,
    })
  }
  switch (rest) {
    case "keys":
      return answer({ path: `${user.home}/.ssh/authorized_keys`, keys: keysOf[name] ?? [] })
    case "sudo":
      return answer(sudoOf[name] ?? { rules: [], managed: { level: "none", commands: [] } })
    case "disk":
      return answer({ bytes: name === "ion" ? 3_400_000_000 : 41_000 })
    case "activity":
      return answer(activity[name] ?? { sessions: [], processes: 0, busiest: [], cronJobs: 0 })
    case "history":
      return answer(history[name] ?? { logins: [], failed: [], sudo: [] })
    case "limits":
      return answer({ cpuPercent: 0, memoryBytes: 0, tasks: 0, systemd: true })
    case "removal":
      return answer(
        removals[name] ?? {
          username: name,
          home: user.home,
          homeExists: true,
          homeBytes: 41_000,
          homeUnsafe: false,
          processes: 0,
          cronJobs: false,
          sshKeys: user.sshKeyCount,
          sudoRules: [],
          elsewhere: [],
          elsewhereTruncated: false,
        },
      )
  }
  return false
}

export async function mockSystemUsers(page: Page, hostname = "web-01") {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/updates/self") return json(route, { current: "0.8.2", latest: "0.8.2" })
    if (path === "/system/host") {
      return json(route, { hostname, platform: "ubuntu", platformVersion: "24.04" })
    }
    if (path === "/audit") return json(route, audit)
    if (await answerSystemUsers(route)) return
    return json(route, [])
  })
}

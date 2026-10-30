import type { DotTone } from "@/components/status-dot"
import type { RootRoute, SystemUser } from "@/lib/types"

/**
 * What an account's facts mean, in sentences. The server says what it found and
 * which single thing closes each way in; turning that into words is the page's,
 * and it is all here so it can be tested without a page.
 */

const WHY: Record<string, string> = {
  disabled: "The account is disabled.",
  no_shell: "Its shell refuses a login.",
  no_ssh: "This server has no SSH server.",
  not_allowed: "SSH's allow and deny lists keep it out.",
  root_login_off: "SSH refuses root on this server.",
  root_keys_only: "SSH only lets root in with a key.",
  password_auth_off: "SSH refuses passwords on this server.",
  key_auth_off: "SSH refuses keys on this server.",
  no_password: "It has no password.",
  password_locked: "Its password is locked.",
  empty_refused: "Its password is empty, which SSH refuses.",
  no_keys: "It has no SSH keys.",
  locked_without_pam: "SSH treats a locked password as a locked account here.",
  password_dead: "Its password expired too long ago, which closes the account.",
  keys_unknown: "It has a key file this could not read, so it may well get in.",
}

export function whyClosed(why: string | undefined) {
  return (why && WHY[why]) || "SSH would not let it in."
}

export type SignInSummary = {
  tone: DotTone
  label: string
  /** Why, when the answer is no. */
  detail?: string
  password: boolean
  key: boolean
}

/** Whether the account can get in, and how. The list reads this and nothing else. */
export function signInSummary(user: SystemUser): SignInSummary {
  const password = user.signIn.password.allowed
  const key = user.signIn.key.allowed
  if (user.disabled) return { tone: "danger", label: "Disabled", password, key }
  if (user.passwordDead) return { tone: "danger", label: "Password expired", password, key }
  if (password || key) {
    if (user.noPassword && password) {
      return { tone: "danger", label: "No password needed", password, key }
    }
    return { tone: "running", label: "Can sign in", password, key }
  }
  if (!user.canLogin) return { tone: "unknown", label: "No login", password, key }
  return {
    tone: user.system ? "unknown" : "warning",
    label: "No way in",
    detail: `${whyClosed(user.signIn.password.why)} ${whyClosed(user.signIn.key.why)}`,
    password,
    key,
  }
}

export type RootSummary = {
  /** `danger` is root with nothing asked; `muted` is a route that goes nowhere. */
  tone: "danger" | "warning" | "default" | "muted"
  label: string
  /** Groups that are root by another name, each drawn as its product. */
  groups: string[]
}

function sudoLabel(route: RootRoute): Pick<RootSummary, "tone" | "label"> {
  if (route.limited) return { tone: "default", label: "Some commands" }
  if (!route.usable) return { tone: "muted", label: "Needs a password it lacks" }
  if (route.noPassword) return { tone: "danger", label: "No password asked" }
  return { tone: "warning", label: "Asks for password" }
}

const SUDO_RANK = (route: RootRoute) =>
  route.limited ? 3 : !route.usable ? 2 : route.noPassword ? 0 : 1

/** How far this account can reach, the strongest route first. */
export function rootSummary(user: Pick<SystemUser, "rootRoutes">): RootSummary {
  const groups = user.rootRoutes.filter((r) => r.kind === "group").map((r) => r.via ?? "")
  if (user.rootRoutes.some((r) => r.kind === "uid0")) {
    return { tone: "danger", label: "Is root", groups }
  }
  const sudo = user.rootRoutes
    .filter((r) => r.kind === "sudo")
    .sort((a, b) => SUDO_RANK(a) - SUDO_RANK(b))[0]
  if (sudo) return { ...sudoLabel(sudo), groups }
  if (groups.length > 0) return { tone: "warning", label: "Through a group", groups }
  return { tone: "muted", label: "None", groups }
}

/** Anything that reaches root, whether or not this account can use it today. */
export function reachesRoot(user: Pick<SystemUser, "rootRoutes">) {
  return user.rootRoutes.some((route) => !route.limited)
}

export type AccountFinding = {
  id: string
  tone: DotTone
  title: string
  detail?: string
  href: string
  action: string
}

export function accountHref(username: string) {
  return `/system-users/user/${encodeURIComponent(username)}`
}

function nameRun(names: string[]) {
  if (names.length <= 2) return names.join(" and ")
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
}

/**
 * What the list opens on, and only when there is something: each is a thing the
 * operator would change, with where to change it. A host with nothing to say
 * draws nothing, so there is no green line to learn to skip.
 */
export function accountFindings(users: SystemUser[]): AccountFinding[] {
  const findings: AccountFinding[] = []
  for (const user of users) {
    if (user.noPassword && user.signIn.password.allowed) {
      findings.push({
        id: `empty:${user.username}`,
        tone: "danger",
        title: `${user.username} signs in with no password`,
        detail: "Anyone who reaches SSH gets in.",
        href: accountHref(user.username),
        action: "Set one",
      })
    }
  }
  const root = users.find((user) => user.uid === 0)
  if (root?.signIn.password.allowed) {
    findings.push({
      id: "root-password",
      tone: "warning",
      title: "root signs in with a password",
      detail: root.signIn.key.allowed
        ? "A key already works, so passwords can be turned off for it."
        : "Bots guess at this all day. Add a key, then turn password sign-in off.",
      href: root.signIn.key.allowed ? "/security/ssh" : accountHref("root"),
      action: root.signIn.key.allowed ? "SSH settings" : "Add a key",
    })
  }
  const passwordless = users.filter((user) =>
    user.rootRoutes.some((r) => r.kind === "sudo" && r.noPassword && !r.limited),
  )
  if (passwordless.length > 0) {
    const first = passwordless[0].rootRoutes.find((r) => r.kind === "sudo" && r.noPassword)
    findings.push({
      id: "sudo-nopassword",
      tone: "warning",
      title: `${nameRun(passwordless.map((u) => u.username))} can become root with no password`,
      detail: first?.file ? `From ${first.file}` : undefined,
      href: accountHref(passwordless[0].username),
      action: "Review",
    })
  }
  const stranded = users.filter(
    (user) =>
      !user.system && !user.disabled && !user.signIn.password.allowed && !user.signIn.key.allowed,
  )
  if (stranded.length > 0) {
    findings.push({
      id: "no-way-in",
      tone: "notice",
      title: `${nameRun(stranded.map((u) => u.username))} ${stranded.length === 1 ? "has" : "have"} no way in`,
      detail: "No usable password or key, so nobody can sign in as them.",
      href: accountHref(stranded[0].username),
      action: stranded.length === 1 ? "Give one" : "Open",
    })
  }
  return findings
}

/** The newest sign-in on the host, for the line at the top of the page. */
export function latestSignIn(users: SystemUser[]) {
  return users
    .filter((user) => user.lastLogin)
    .sort(
      (a, b) =>
        new Date(b.lastLogin as string).getTime() - new Date(a.lastLogin as string).getTime(),
    )[0]
}

/**
 * A key's restrictions in words, for a line under its name. `options` is the
 * comma-joined text of the file, and a comma inside quotes is not a separator.
 */
export function splitKeyOptions(options: string | undefined) {
  const out: string[] = []
  let current = ""
  let quoted = false
  for (const char of options ?? "") {
    if (char === '"') quoted = !quoted
    if (char === "," && !quoted) {
      out.push(current)
      current = ""
      continue
    }
    current += char
  }
  if (current) out.push(current)
  return out
}

export type KeyRestrictions = {
  from: string
  command: string
  expires: string
  restrict: boolean
  /** Options this form has no field for, kept as they are. */
  other: string[]
}

const quotedValue = (option: string) => option.slice(option.indexOf("=") + 1).replace(/^"|"$/g, "")

export function readRestrictions(options: string | undefined): KeyRestrictions {
  const out: KeyRestrictions = { from: "", command: "", expires: "", restrict: false, other: [] }
  for (const option of splitKeyOptions(options)) {
    if (option === "restrict") out.restrict = true
    else if (option.startsWith("from=")) out.from = quotedValue(option)
    else if (option.startsWith("command=")) out.command = quotedValue(option)
    else if (option.startsWith("expiry-time=")) {
      const value = quotedValue(option)
      out.expires = /^\d{8}/.test(value)
        ? `${value.slice(0, 4)}-${value.slice(4, 6)}-${value.slice(6, 8)}`
        : ""
    } else out.other.push(option)
  }
  return out
}

/** The form's answers back into what authorized_keys says. */
export function writeRestrictions(r: KeyRestrictions) {
  const out = [...r.other]
  if (r.restrict) out.unshift("restrict")
  if (r.from.trim()) out.push(`from="${r.from.trim()}"`)
  if (r.command.trim()) out.push(`command="${r.command.trim()}"`)
  if (r.expires) out.push(`expiry-time="${r.expires.replaceAll("-", "")}"`)
  return out
}

/** One short phrase per restriction, for the row. */
export function restrictionWords(options: string | undefined) {
  const r = readRestrictions(options)
  const out: string[] = []
  if (r.restrict) out.push("No forwarding")
  if (r.from) out.push(`Only from ${r.from}`)
  if (r.command) out.push("One command")
  if (r.expires) out.push(`Expires ${r.expires}`)
  return out.concat(r.other)
}

/**
 * The group `useradd` made for one account, which no one else is in. A host has
 * one per person and none of them is ever the group somebody came looking for.
 */
export function isPrivateGroup(group: { name: string; primary: string[]; members: string[] }) {
  return group.members.length === 0 && group.primary.length === 1 && group.primary[0] === group.name
}

/** What being in a group hands over, for the groups a reader might not know. */
export function groupGrant(name: string): string | undefined {
  switch (name) {
    case "docker":
      return "Root by another name: can mount the host's files into a container"
    case "lxd":
    case "lxc":
      return "Root by another name: can mount the host's files into a container"
    case "libvirt":
      return "Root by another name: can attach the host's disks to a machine"
    case "disk":
      return "Root by another name: reads and writes the disk directly"
    case "adm":
      return "Reads the system logs"
    case "systemd-journal":
      return "Reads the journal"
    case "www-data":
      return "Shares the web server's files"
    case "shadow":
      return "Reads every password hash"
  }
  return undefined
}

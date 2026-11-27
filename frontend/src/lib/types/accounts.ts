/*
 * Who signs in and how: the dashboard's accounts, roles, sessions, passkeys
 * and API keys, and the server's own users, groups, sudo rules and password
 * aging.
 */

import type { Capability } from "./core"
import type { HubLinkSession } from "./dashboard"

export type Role = "admin" | "limited" | "readonly"

export type DashboardUser = {
  id: number
  /** The sign-in key: lower case, one word, what the audit log names. */
  username: string
  /** The name shown — the username as typed at creation until it is changed. */
  displayName: string
  /** When the picture was last set, or 0 for none; the avatar URL's cache key. */
  avatarVersion: number
  role: Role
  totpEnabled: boolean
  disabled: boolean
  mustChangePassword: boolean
  /** Absent for an account that has never signed in, and in the answer to its first sign-in. */
  lastLoginAt?: string
  createdAt: string
  /** Not yet asked what the dashboard is for; the shell sends the account to `/welcome`. */
  needsOnboarding: boolean
  /** Sections kept out of this account's sidebar (`lib/sidebar-sections.ts`). */
  hiddenSections: string[]
}

export type AuthStatus = {
  authenticated: boolean
  user?: DashboardUser
  capabilities?: Capability[]
  needsTotp: boolean
  needsEnrollment: boolean
  needsPasswordChange?: boolean
  require2fa: boolean
  /** Present only when this server is linked to a Just Hub; see `HubLinkSession`. */
  hub?: HubLinkSession
}

export type PasswordState = "set" | "locked" | "none" | "empty" | "unknown"

export type SignInRoute = { allowed: boolean; why?: string }

export type RootRoute = {
  kind: "uid0" | "sudo" | "group"
  via?: string
  noPassword?: boolean
  limited?: boolean
  ruleId?: string
  file?: string
  /** False for a rule that asks for a password the account does not have. */
  usable: boolean
}

export type PasswordAging = {
  lastChanged?: string
  mustChange: boolean
  minDays?: number
  maxDays?: number
  warnDays?: number
  inactiveDays?: number
  expiresOn?: string
}

export type SystemUser = {
  username: string
  uid: number
  gid: number
  comment: string
  home: string
  shell: string
  groups: string[]
  primaryGroup?: string
  system: boolean
  locked: boolean
  noPassword: boolean
  lastLogin?: string
  lastLoginFrom?: string
  sshKeyCount: number
  /** A key source that could not be read, so the count is a floor. */
  keysUnknown?: boolean
  canLogin: boolean
  password: PasswordState
  disabled: boolean
  /** Closed by its password's age rules, which clearing an expiry does not undo. */
  passwordDead?: boolean
  expiresOn?: string
  aging: PasswordAging
  signIn: { password: SignInRoute; key: SignInRoute }
  rootRoutes: RootRoute[]
  protected: boolean
}

export type SystemUserDetail = SystemUser & {
  failedAttempts: { available: boolean; count: number }
  shells: string[]
}

export type SSHKey = {
  line: number
  type: string
  comment: string
  fingerprint: string
  bits?: number
  options?: string
  raw: string
  weak?: string
  lastUsed?: string
  lastUsedFrom?: string
  /** A key in a file this page does not write: sshd reads it, and it cannot be changed here. */
  file?: string
}

export type SudoRule = {
  id: string
  file: string
  line: number
  text: string
  who: string[]
  runAs: string
  commands: string[]
  noPassword: boolean
  full: boolean
  asRoot: boolean
  managed: boolean
}

export type SudoLevel = "none" | "password" | "nopassword"

export type SudoGrant = { level: SudoLevel; commands: string[] }

export type AccountSudo = { rules: SudoRule[]; managed: SudoGrant }

export type AccountSession = {
  user: string
  tty: string
  from: string
  loginTime?: string
  idle?: string
  pid?: number
  command?: string
  isSsh: boolean
}

export type AccountProcess = {
  pid: number
  cpu: number
  memory: number
  seconds: number
  name: string
}

export type AccountActivity = {
  sessions: AccountSession[]
  processes: number
  busiest: AccountProcess[]
  cronJobs: number
}

export type AccountLogin = {
  kind: string
  user: string
  tty: string
  from: string
  loginTime?: string
  endTime?: string
  duration?: string
  active: boolean
}

export type AccountSudoEvent = {
  at: string
  runAs: string
  command: string
  failed: boolean
  reason?: string
}

export type AccountHistory = {
  logins: AccountLogin[]
  failed: AccountLogin[]
  sudo: AccountSudoEvent[]
}

export type AccountRemoval = {
  username: string
  home: string
  homeExists: boolean
  homeBytes: number
  homeUnsafe: boolean
  processes: number
  cronJobs: boolean
  sshKeys: number
  sudoRules: SudoRule[]
  elsewhere: string[]
  elsewhereTruncated: boolean
  /** Another account with this one's number: only the name can be removed. */
  sharesNumber?: string
  blocked?: string
}

export type AccountLimits = {
  cpuPercent: number
  memoryBytes: number
  tasks: number
  systemd: boolean
  quota?: { mount: string; usedBytes: number; softBytes: number; hardBytes: number }
}

export type SystemGroup = {
  name: string
  gid: number
  members: string[]
  primary: string[]
  system: boolean
  rootEquivalent: boolean
  sudoRules: SudoRule[]
  protected: boolean
}

export type AccountDefaults = {
  passwordMaxDays: number
  passwordMinDays: number
  passwordWarnDays: number
  shell: string
  inactiveDays: number
  quality: { available: boolean; minLength: number; minClasses: number }
  skel: { name: string; bytes: number; dir: boolean }[]
  shells: string[]
}

export type AccountTemplate = {
  id: string
  name: string
  builtin: boolean
  shell: string
  groups: string[]
  sudo: SudoLevel
  system: boolean
  noHome: boolean
}

export type BulkResults = { results: { username: string; error?: string }[] }

export type ApiToken = {
  id: number
  userId: number
  name: string
  prefix: string
  role: Role
  /**
   * The one capability the key is limited to, `mcp` for a key that opens the
   * MCP endpoint alone, or `deploy` for a key that reaches the deployment
   * routes alone; empty for an ordinary key.
   */
  scope?: "" | "metrics.read" | "mcp" | "deploy"
  createdAt: string
  expiresAt?: string
  lastUsedAt?: string
  revoked: boolean
  /** The databases an MCP key reaches. Every other key has none. */
  connections?: ApiTokenConnection[]
  /** What an MCP key may do with the deployments; absent is nothing. */
  deployments?: ApiTokenDeployments
  /**
   * The deployments a deploy key, or an MCP key's deployment grant, is
   * limited to. Absent or empty is every deployment, including the ones made
   * after the key.
   */
  deploymentIds?: number[]
  /**
   * Whether an MCP key is offered the tools generated from the deployment
   * API under its grant; absent for every key made before them.
   */
  operations?: boolean
}

/** An MCP key's deployment grant: nothing, read them, or also deploy and roll back. */
export type ApiTokenDeployments = "" | "read" | "deploy"

/** One database an MCP key reaches, to read only or to change as well. */
export type ApiTokenConnection = {
  connectionId: number
  access: "read" | "write"
}

export type SessionInfo = {
  authMethod?:
    | "unknown"
    | "password"
    | "totp"
    | "recovery"
    | "passkey"
    | "passkey+totp"
    | "passkey+recovery"
    | "hub"
  /** The Just Hub user whose grant opened a `hub` session (hub-link-protocol §9.9). */
  hubUser?: string
  id: string
  userId: number
  twoFactorPassed: boolean
  ip: string
  userAgent: string
  createdAt: string
  lastSeenAt: string
  expiresAt: string
  current: boolean
}

/**
 * A passkey of the signed-in account: a WebAuthn credential that stands in
 * for its password at sign-in. `rpId` is the host name it signs in at.
 */
export type Passkey = {
  id: number
  name: string
  rpId: string
  createdAt: string
  /** Absent when it has never signed in. */
  lastUsedAt?: string
  /** Backed up by a password manager or a phone's keychain rather than held on one device. */
  synced: boolean
  transports: string[]
}

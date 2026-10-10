import { buildDsn } from "@/lib/db-dsn"
import { plural } from "@/lib/format"
import type { DbConnection } from "@/lib/types"
import type {
  DbGrant,
  DbPrivilegeLevel,
  DbPrivilegeRequest,
  DbRole,
  DbRoleAlterRequest,
  DbRoleDetail,
  MongoRoleRef,
  RedisACLRequest,
  RedisACLUser,
} from "@/components/database/ops/access-types"

/**
 * What the Access page works out from what the server lists: which account is
 * the dashboard's own, what an account's grants add up to, what the privilege
 * matrix holds and which requests a set of presses becomes, and the string a
 * new account connects with. No React, so each rule can be held to its
 * inputs.
 */

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

/** One account among its neighbours: MySQL has one per name and host. */
export function accountKey(role: Pick<DbRole, "name" | "host">): string {
  return role.host ? `${role.name}@${role.host}` : role.name
}

/**
 * Whether an account is the one this connection signs in with. The server
 * compares the name alone, without its case and without a MySQL host, and
 * refuses to lock it out; the page marks the same accounts.
 */
export function isOwnAccount(
  role: Pick<DbRole, "name">,
  conn: Pick<DbConnection, "user">,
): boolean {
  return Boolean(conn.user) && role.name.toLowerCase() === conn.user.toLowerCase()
}

/** It can sign in: it has a login and is not locked. */
export function signsIn(role: Pick<DbRole, "login" | "locked">): boolean {
  return role.login && !role.locked
}

export type AccountShow = "" | "admins" | "sessions" | "blocked"

export function accountCounts(roles: readonly DbRole[]) {
  return {
    all: roles.length,
    admins: roles.filter((role) => role.superuser).length,
    sessions: roles.filter((role) => role.connections > 0).length,
    connections: roles.reduce((sum, role) => sum + Math.max(0, role.connections), 0),
    blocked: roles.filter((role) => !signsIn(role)).length,
  }
}

export function filterAccounts(
  roles: readonly DbRole[],
  show: AccountShow,
  search: string,
): DbRole[] {
  const words = search.trim().toLowerCase()
  return roles.filter((role) => {
    if (show === "admins" && !role.superuser) return false
    if (show === "sessions" && role.connections <= 0) return false
    if (show === "blocked" && signsIn(role)) return false
    return !words || accountKey(role).toLowerCase().includes(words)
  })
}

export type AccountTag = { word: string; tone?: "warning" }

/** What an account is, as the words at its row's edge. */
export function accountTags(role: DbRole, own: boolean): AccountTag[] {
  const tags: AccountTag[] = []
  if (own) tags.push({ word: "this connection" })
  if (role.system) tags.push({ word: "system" })
  if (role.superuser) tags.push({ word: "administrator" })
  if (role.locked) tags.push({ word: "locked", tone: "warning" })
  else if (!role.login) tags.push({ word: "no login" })
  if (!role.superuser && role.createDb) tags.push({ word: "creates databases" })
  if (!role.superuser && role.createRole) tags.push({ word: "creates accounts" })
  return tags
}

/** Every grant by who holds it: `name`, or `name@host` where the server says a host. */
export function grantsByAccount(grants: readonly DbGrant[]): Map<string, DbGrant[]> {
  const map = new Map<string, DbGrant[]>()
  for (const grant of grants) {
    const key = grant.host ? `${grant.grantee}@${grant.host}` : grant.grantee
    const held = map.get(key)
    if (held) held.push(grant)
    else map.set(key, [grant])
  }
  return map
}

/**
 * What an account's grants add up to, in a line: how many databases, schemas
 * and tables it was given something on. What it holds by owning an object is
 * counted apart, since no revoke takes that away.
 */
export function grantSummary(
  role: Pick<DbRole, "superuser" | "memberOf">,
  grants: readonly DbGrant[],
): string {
  if (role.superuser) return "Everything on the server"
  const given = grants.filter((grant) => !grant.owner && !grant.future)
  const everywhere = given.find(
    (grant) => grant.level === "server" && grant.privileges.some((one) => /^ALL\b/i.test(one)),
  )
  if (everywhere) return "Every privilege on the server"
  const count = (level: DbGrant["level"]) =>
    new Set(
      given
        .filter((grant) => grant.level === level)
        .map((grant) => [grant.database, grant.schema, grant.table].join("\u0000")),
    ).size
  const objects: string[] = []
  const databases = count("database")
  const schemas = count("schema")
  const tables = count("table")
  const sequences = count("sequence")
  if (databases) objects.push(plural(databases, "database"))
  if (schemas) objects.push(plural(schemas, "schema"))
  if (tables) objects.push(plural(tables, "table"))
  if (sequences) objects.push(plural(sequences, "sequence"))
  const parts: string[] = []
  if (objects.length > 0) parts.push(`Granted on ${objects.join(", ")}`)
  else {
    const server = given
      .filter((grant) => grant.level === "server")
      .flatMap((grant) => grant.privileges)
      // USAGE is the engine's word for "may sign in": it grants nothing.
      .filter((one) => one.toUpperCase() !== "USAGE")
    if (server.length > 0) parts.push(plural(server.length, "server privilege"))
  }
  const owned = grants.filter((grant) => grant.owner).length
  if (owned) parts.push(`owns ${plural(owned, "object")}`)
  if (role.memberOf && role.memberOf.length > 0) {
    parts.push(`member of ${role.memberOf.join(", ")}`)
  }
  if (parts.length === 0) return "No grant of its own"
  const line = parts.join(" · ")
  return line[0].toUpperCase() + line.slice(1)
}

/** An account's name as the create route takes it. */
const ACCOUNT_NAME = /^[A-Za-z_][A-Za-z0-9_.-]{0,62}$/

export function accountNameProblem(name: string, taken: readonly string[]): string | undefined {
  const typed = name.trim()
  if (!typed) return "An account needs a name."
  if (!ACCOUNT_NAME.test(typed)) {
    return "Letters, digits, dots, dashes and underscores, starting with a letter or an underscore."
  }
  if (taken.some((other) => other.toLowerCase() === typed.toLowerCase())) {
    return "An account of that name already exists."
  }
  return undefined
}

/** A switch of the account's panel, and the request field it sets. */
export type AttributeField = keyof Omit<
  DbRoleAlterRequest,
  "host" | "password" | "connectionLimit" | "validUntil"
>

export const ATTRIBUTES: { field: AttributeField; title: string; hint?: string }[] = [
  { field: "login", title: "It can sign in" },
  { field: "locked", title: "Locked: it cannot sign in until it is unlocked" },
  {
    field: "superuser",
    title: "Administrator of the whole server",
    hint: "Every privilege on every database, whatever its grants say.",
  },
  { field: "createDb", title: "It can create databases" },
  { field: "createRole", title: "It can create and change other accounts" },
  {
    field: "inherit",
    title: "It uses the privileges of the roles it is a member of",
  },
  { field: "replication", title: "It can start replication and take base backups" },
  { field: "bypassRls", title: "It bypasses row-level security" },
]

/** What an attribute is now, from wherever the detail keeps it. */
export function attributeValue(detail: DbRoleDetail, field: AttributeField): boolean {
  switch (field) {
    case "login":
      return detail.login
    case "superuser":
      return detail.superuser
    case "createDb":
      return detail.createDb
    case "createRole":
      return detail.createRole
    case "locked":
      return Boolean(detail.locked ?? detail.attributes.locked)
    default:
      return Boolean(detail.attributes[field])
  }
}

/**
 * Why a switch may not be thrown on the dashboard's own account: the server
 * refuses a change that would cut the connection off from the server it is
 * administering, and the page says so in the switch's place.
 */
export function lockoutReason(
  field: AttributeField,
  next: boolean,
  own: boolean,
): string | undefined {
  if (!own) return undefined
  if (field === "login" && !next) return "This connection signs in with it."
  if (field === "locked" && next) return "This connection signs in with it."
  if (field === "superuser" && !next) return "This connection administers the server with it."
  return undefined
}

// ---------------------------------------------------------------------------
// The privilege matrix
// ---------------------------------------------------------------------------

/** One object a privilege is held on. */
export type Cell = {
  level: DbPrivilegeLevel["level"]
  database?: string
  schema?: string
  table?: string
}

export function cellKey(cell: Cell): string {
  return [cell.level, cell.database ?? "", cell.schema ?? "", cell.table ?? ""].join("\u0000")
}

/** The privileges a level's chips stand for: every one but the word for all of them. */
export function levelPrivileges(level: Pick<DbPrivilegeLevel, "privileges">): string[] {
  return level.privileges.filter((one) => one.toUpperCase() !== "ALL")
}

export type Held = {
  /** Privileges of the level's own set that are held, by grant or by owning. */
  privileges: string[]
  /** Held by owning the object: a revoke does not take these away. */
  owned: string[]
  /** May be granted on to others. */
  grantable: boolean
  /** What else the server lists on it that the chips cannot say: a column grant, a deny. */
  other: string[]
}

function sameObject(grant: DbGrant, cell: Cell): boolean {
  if (grant.level !== cell.level || grant.future) return false
  if (cell.database !== undefined && (grant.database ?? "") !== cell.database) return false
  if (cell.schema !== undefined && (grant.schema ?? "") !== cell.schema) return false
  return (grant.table ?? "") === (cell.table ?? "")
}

/** What an account holds on one object, read off its grants. */
export function heldOn(
  grants: readonly DbGrant[],
  cell: Cell,
  level: Pick<DbPrivilegeLevel, "privileges">,
): Held {
  const domain = levelPrivileges(level)
  const known = new Map(domain.map((one) => [one.toUpperCase(), one]))
  const held = new Set<string>()
  const owned = new Set<string>()
  const other = new Set<string>()
  let grantable = false
  for (const grant of grants) {
    if (!sameObject(grant, cell)) continue
    if (grant.grantable) grantable = true
    for (const privilege of grant.privileges) {
      const upper = privilege.toUpperCase()
      const names =
        upper === "ALL" || upper === "ALL PRIVILEGES"
          ? domain
          : known.has(upper)
            ? [known.get(upper)!]
            : []
      if (names.length === 0) {
        other.add(privilege)
        continue
      }
      for (const name of names) {
        held.add(name)
        if (grant.owner) owned.add(name)
      }
    }
  }
  // One both owned and granted is granted: revoking it does something.
  for (const grant of grants) {
    if (!sameObject(grant, cell) || grant.owner) continue
    for (const privilege of grant.privileges) {
      const upper = privilege.toUpperCase()
      if (upper === "ALL" || upper === "ALL PRIVILEGES") owned.clear()
      else if (known.has(upper)) owned.delete(known.get(upper)!)
    }
  }
  return {
    privileges: domain.filter((one) => held.has(one)),
    owned: domain.filter((one) => owned.has(one)),
    grantable,
    other: [...other],
  }
}

/** The privileges an account holds for objects created later in a schema. */
export function heldLater(
  grants: readonly DbGrant[],
  scope: Pick<Cell, "level" | "database" | "schema">,
  level: Pick<DbPrivilegeLevel, "privileges">,
): string[] {
  const domain = levelPrivileges(level)
  const held = new Set<string>()
  for (const grant of grants) {
    if (!grant.future || grant.level !== scope.level) continue
    if (scope.schema !== undefined && (grant.schema ?? "") !== scope.schema) continue
    for (const privilege of grant.privileges) {
      const upper = privilege.toUpperCase()
      if (upper === "ALL" || upper === "ALL PRIVILEGES") domain.forEach((one) => held.add(one))
      else held.add(domain.find((one) => one.toUpperCase() === upper) ?? privilege)
    }
  }
  return domain.filter((one) => held.has(one))
}

/** What the reader wants an object to hold, where that differs from what it holds. */
export type Wanted = Record<string, { cell: Cell; privileges: string[] }>

/** A press on one privilege of one object: it is wanted if it was not, and the other way round. */
export function toggled(wanted: Wanted, cell: Cell, held: Held, privilege: string): Wanted {
  const key = cellKey(cell)
  const now = wanted[key]?.privileges ?? held.privileges
  // What is held by owning stays held: there is nothing to press.
  if (held.owned.includes(privilege)) return wanted
  const next = now.includes(privilege)
    ? now.filter((one) => one !== privilege)
    : [...now, privilege]
  return settled(wanted, cell, held, next)
}

/** Every privilege of the level on one object, or none of them but what it owns. */
export function toggledAll(
  wanted: Wanted,
  cell: Cell,
  held: Held,
  level: Pick<DbPrivilegeLevel, "privileges">,
): Wanted {
  const domain = levelPrivileges(level)
  const now = wanted[cellKey(cell)]?.privileges ?? held.privileges
  const full = domain.every((one) => now.includes(one))
  return settled(wanted, cell, held, full ? [...held.owned] : domain)
}

function settled(wanted: Wanted, cell: Cell, held: Held, next: string[]): Wanted {
  const key = cellKey(cell)
  const same =
    next.length === held.privileges.length && next.every((one) => held.privileges.includes(one))
  const rest = { ...wanted }
  if (same) delete rest[key]
  else rest[key] = { cell, privileges: next }
  return rest
}

/** A membership the reader wants added or taken away. */
export type Membership = { role: string; member: boolean }

/** One request the page will send, and whether it grants or revokes. */
export type Planned = {
  action: "grant" | "revoke"
  body: DbPrivilegeRequest
  /** What it is about, in a sentence: for a refusal that names it. */
  about: string
  /** The same in two parts — the act, and the object as the engine writes it. */
  act: string
  on: string
}

export type PlanOptions = {
  host?: string
  /** Grants are made WITH GRANT OPTION. */
  grantOption?: boolean
  /** A whole-schema change also covers objects created later. */
  future?: boolean
}

/** An object as a kind and its name: "table" and "public.orders". */
function objectWords(cell: Cell): { kind: string; name: string } {
  if (cell.level === "database") return { kind: "database", name: cell.database ?? "" }
  if (cell.level === "schema") return { kind: "schema", name: cell.schema ?? "" }
  const scope = cell.schema ?? cell.database ?? ""
  const kind = cell.level === "sequence" ? "sequence" : "table"
  if (!cell.table) return { kind: `every ${kind} in`, name: scope }
  return { kind, name: `${scope ? `${scope}.` : ""}${cell.table}` }
}

/**
 * The requests a set of wanted changes becomes.
 *
 * One object, one grant and one revoke at most, each naming exactly what was
 * pressed — every privilege of the level at once is its word `ALL`. And
 * where the same privilege was pressed on every object of a schema, the
 * schema is asked for once rather than each table in turn — which is also the
 * only request that can cover the tables created later.
 */
export function plannedRequests(
  wanted: Wanted,
  holds: (cell: Cell) => Held,
  levels: readonly DbPrivilegeLevel[],
  /** Every object of a scope the matrix lists, by `scopeKey`. */
  listed: Record<string, string[]>,
  memberships: readonly Membership[],
  options: PlanOptions = {},
): Planned[] {
  const planned: Planned[] = []
  const base = options.host ? { host: options.host } : {}
  const byLevel = new Map(levels.map((level) => [level.level, level]))

  type Change = { cell: Cell; grant: string[]; revoke: string[] }
  const changes: Change[] = Object.values(wanted).flatMap(({ cell, privileges }) => {
    const level = byLevel.get(cell.level)
    if (!level) return []
    const held = holds(cell)
    const grant = privileges.filter((one) => !held.privileges.includes(one))
    const revoke = held.privileges.filter(
      (one) => !privileges.includes(one) && !held.owned.includes(one),
    )
    if (grant.length === 0 && revoke.length === 0) return []
    return [{ cell, grant, revoke }]
  })

  // A privilege pressed the same way on every listed object of a schema.
  const whole = new Map<string, { cell: Cell; grant: Set<string>; revoke: Set<string> }>()
  for (const [scope, objects] of Object.entries(listed)) {
    if (objects.length < 2) continue
    const [levelName, database, schema] = scope.split("\u0000")
    const level = byLevel.get(levelName as Cell["level"])
    if (!level?.allObjects) continue
    const inScope = changes.filter(
      (change) =>
        change.cell.level === levelName &&
        (change.cell.database ?? "") === database &&
        (change.cell.schema ?? "") === schema &&
        Boolean(change.cell.table),
    )
    if (inScope.length !== objects.length) continue
    const everywhere = (pick: (change: Change) => string[]) =>
      levelPrivileges(level).filter((one) => inScope.every((change) => pick(change).includes(one)))
    const grant = everywhere((change) => change.grant)
    const revoke = everywhere((change) => change.revoke)
    if (grant.length === 0 && revoke.length === 0) continue
    whole.set(scope, {
      cell: {
        level: levelName as Cell["level"],
        ...(database ? { database } : {}),
        ...(schema ? { schema } : {}),
        table: "",
      },
      grant: new Set(grant),
      revoke: new Set(revoke),
    })
    for (const change of inScope) {
      change.grant = change.grant.filter((one) => !grant.includes(one))
      change.revoke = change.revoke.filter((one) => !revoke.includes(one))
    }
  }

  // Every privilege of the level at once is asked for by its own word, where
  // the level has one: that is what the routes take for it.
  const send = (action: Planned["action"], cell: Cell, privileges: string[]) => {
    if (privileges.length === 0) return
    const level = byLevel.get(cell.level)
    const all =
      Boolean(level?.privileges.some((one) => one.toUpperCase() === "ALL")) &&
      level !== undefined &&
      levelPrivileges(level).every((one) => privileges.includes(one))
    const future = options.future && level?.future && cell.table === ""
    const object = objectWords(cell)
    const act = `${action === "grant" ? "Grant on" : "Revoke on"} ${object.kind}`
    planned.push({
      action,
      about: `${act} ${object.name}`,
      act,
      on: object.name,
      body: {
        ...base,
        level: cell.level,
        ...(cell.database !== undefined ? { database: cell.database } : {}),
        ...(cell.schema !== undefined ? { schema: cell.schema } : {}),
        ...(cell.table !== undefined ? { table: cell.table } : {}),
        privileges: all ? ["ALL"] : privileges,
        ...(action === "grant" && options.grantOption && level?.grantOption
          ? { grantOption: true }
          : {}),
        ...(future ? { future: true } : {}),
      },
    })
  }

  for (const { cell, grant, revoke } of whole.values()) {
    send("revoke", cell, [...revoke])
    send("grant", cell, [...grant])
  }
  for (const change of changes) {
    send("revoke", change.cell, change.revoke)
    send("grant", change.cell, change.grant)
  }
  for (const membership of memberships) {
    const act = membership.member ? "Make it a member of" : "Take it out of"
    planned.push({
      action: membership.member ? "grant" : "revoke",
      about: `${act} ${membership.role}`,
      act,
      on: membership.role,
      body: { ...base, level: "role", memberOf: membership.role },
    })
  }
  // Revokes first: a grant that follows a revoke of the same object stands.
  return planned.sort((a, b) => Number(a.action === "grant") - Number(b.action === "grant"))
}

/** The objects of one schema (or database) at one level, as the key `plannedRequests` lists them by. */
export function scopeKey(scope: Pick<Cell, "level" | "database" | "schema">): string {
  return [scope.level, scope.database ?? "", scope.schema ?? ""].join("\u0000")
}

/** How many changes are waiting, in the change bar's words. */
export function pendingWords(wanted: Wanted, memberships: readonly Membership[]): string {
  const count = Object.keys(wanted).length + memberships.length
  return count === 1 ? "1 change not applied" : `${count} changes not applied`
}

// ---------------------------------------------------------------------------
// The string a new account connects with
// ---------------------------------------------------------------------------

/**
 * The connection string for an account on this connection's server: the same
 * address, the account's own name and password, and the database it was
 * given. It is written from what the page holds, so it carries no option the
 * saved string may have — the engine's own defaults apply — and reading the
 * saved one, which is recorded, is not needed to make an account.
 */
export function accountDsn(
  conn: Pick<DbConnection, "driver" | "host" | "port" | "database">,
  account: { user: string; password: string; database?: string; authSource?: string },
): string {
  const dsn = buildDsn(conn.driver, {
    host: conn.host,
    port: conn.port,
    user: account.user,
    password: account.password,
    database: account.database ?? conn.database,
    option: account.authSource ?? "",
  })
  // `buildDsn` writes a transport default for the one engine that has it in
  // its option; a new account's string leaves that to the client.
  return dsn.replace(/\?sslmode=disable$/, "")
}

// ---------------------------------------------------------------------------
// A key–value server's ACL users
// ---------------------------------------------------------------------------

/** What a user may do, in a line: the rule without its on/off and its flags. */
export function aclSummary(user: RedisACLUser): string {
  if (user.unrestricted) return "Every command on every key"
  const commands = (user.commands ?? []).join(" ") || "no command"
  const keys = (user.keys ?? []).join(" ") || "no key"
  return `${commands} on ${keys}`
}

/** A list typed one to a line, or separated by spaces. */
export function ruleList(text: string): string[] {
  return text
    .split(/\s+/)
    .map((one) => one.trim())
    .filter(Boolean)
}

export type AclDraft = {
  enabled: boolean
  /** "" keeps what is set. */
  password: string
  noPassword: boolean
  keys: string
  channels: string
  commands: string
}

export function aclDraftOf(user: RedisACLUser | undefined): AclDraft {
  return {
    enabled: user?.enabled ?? true,
    password: "",
    noPassword: user?.noPassword ?? false,
    keys: (user?.keys ?? []).join("\n"),
    channels: (user?.channels ?? []).join("\n"),
    commands: (user?.commands ?? []).filter((one) => one !== "-@all").join("\n"),
  }
}

/**
 * The request a draft becomes: for a new user, everything it states; for one
 * that exists, only the fields that differ — the route leaves an absent field
 * as it is, so a change of the key patterns cannot touch the password.
 */
export function aclRequest(user: RedisACLUser | undefined, draft: AclDraft): RedisACLRequest {
  const saved = aclDraftOf(user)
  const request: RedisACLRequest = user ? {} : { create: true }
  const differs = (a: string, b: string) => ruleList(a).join(" ") !== ruleList(b).join(" ")
  if (!user || draft.enabled !== saved.enabled) request.enabled = draft.enabled
  if (draft.noPassword && (!user || !saved.noPassword)) request.noPassword = true
  else if (draft.password) request.password = draft.password
  if (!user || differs(draft.keys, saved.keys)) request.keys = ruleList(draft.keys)
  if (!user || differs(draft.channels, saved.channels)) request.channels = ruleList(draft.channels)
  if (!user || differs(draft.commands, saved.commands)) request.commands = ruleList(draft.commands)
  return request
}

/** Whether a request changes anything at all. */
export function aclChanged(request: RedisACLRequest): boolean {
  return Object.keys(request).some((key) => key !== "create")
}

// ---------------------------------------------------------------------------
// A document database's users
// ---------------------------------------------------------------------------

/** A role as its users list it: `readWrite@app`. */
export function roleWord(ref: MongoRoleRef): string {
  return `${ref.role}@${ref.db}`
}

export function sameRole(a: MongoRoleRef, b: MongoRoleRef): boolean {
  return a.role === b.role && a.db === b.db
}

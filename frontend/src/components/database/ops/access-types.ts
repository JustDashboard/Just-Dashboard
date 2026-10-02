/**
 * What the account routes answer, per engine family, as the server writes
 * them: a SQL server's roles with their attributes and grants, a key–value
 * server's ACL users, a document database's users and roles.
 */

/** One account of `GET /databases/{id}/server/roles`. */
export type DbRole = {
  name: string
  /** MySQL: the host part of `name@host`. */
  host?: string
  /** It may sign in. */
  login: boolean
  superuser: boolean
  createDb: boolean
  createRole: boolean
  /** -1 = unlimited. */
  connectionLimit: number
  validUntil?: string
  memberOf?: string[]
  /** Sessions it holds now. */
  connections: number
  locked?: boolean
  /** One of the engine's own. */
  system?: boolean
}

export type DbRoles = { roles: DbRole[]; supported: boolean; reason?: string }

export type DbGrantLevel = "server" | "database" | "schema" | "table" | "sequence"

export type DbGrant = {
  /** A role's name, or PUBLIC. */
  grantee: string
  host?: string
  level: DbGrantLevel
  database?: string
  schema?: string
  /** A table or a sequence; empty with `future`. */
  table?: string
  privileges: string[]
  grantable?: boolean
  grantor?: string
  /** Held by owning the object: a revoke does not take it away. */
  owner?: boolean
  /** A default privilege: for objects created later. */
  future?: boolean
}

export type DbGrants = {
  grants: DbGrant[]
  truncated: boolean
  supported: boolean
  reason?: string
}

export type DbRoleDetail = DbRole & {
  attributes: Record<string, boolean>
  /** Roles that are members of this one. */
  members: string[]
  /** Per-role settings (`search_path=app`). */
  config: string[]
  /** Names of per-role settings whose value this viewer is not shown. */
  configRedacted?: string[]
  authPlugin?: string
  grants: DbGrant[]
  grantsTruncated: boolean
  /** Which fields the alter route accepts on this engine. */
  editable: string[]
  notes?: string[]
}

/** Every field is optional: only what is sent changes. */
export type DbRoleAlterRequest = {
  host?: string
  password?: string
  login?: boolean
  superuser?: boolean
  createDb?: boolean
  createRole?: boolean
  connectionLimit?: number
  validUntil?: string
  inherit?: boolean
  replication?: boolean
  bypassRls?: boolean
  locked?: boolean
}

export type DbRoleAltered = { ok: true; connectionUpdated?: boolean }

export type DbPresetLevel = "read" | "write" | "all"

export type DbRoleCreateRequest = DbRoleAlterRequest & {
  name: string
  database?: string
  level?: DbPresetLevel
}

/** What the three-level grant ran, or would run. */
export type DbDatabaseGrantResult = {
  ok?: true
  preview?: true
  statements: string[]
  /** The schemas covered. */
  schemas?: string[]
  /** The schemas left alone, and why. */
  skippedSchemas?: { name: string; reason: string }[]
  notes?: string[]
}

export type DbRoleCreated = DbDatabaseGrantResult & { ok: true }

export type DbPrivilegeLevel = {
  level: "database" | "schema" | "table" | "sequence" | "role"
  /** The only values a request may carry at this level. */
  privileges: string[]
  /** The request fields that name the object. */
  needs: ("database" | "schema" | "table" | "memberOf")[]
  /** The last of `needs` may be empty: every object in the schema. */
  allObjects?: boolean
  /** Also for objects created later. */
  future?: boolean
  grantOption?: boolean
}

export type DbPrivileges = { levels: DbPrivilegeLevel[]; supported: boolean; editable: string[] }

export type DbPrivilegeRequest = {
  host?: string
  level: string
  database?: string
  schema?: string
  table?: string
  privileges?: string[]
  memberOf?: string
  grantOption?: boolean
  future?: boolean
}

export type DbPrivilegeResult = {
  ok?: true
  preview?: true
  statements: string[]
  futureOwners?: string[]
  notes?: string[]
}

/** One schema of `GET /databases/{id}/catalog`, and the objects of the one asked for. */
export type DbCatalog = {
  schema: string
  schemas: { name: string; system?: boolean }[]
  objects: Record<string, { name: string; schema?: string }[] | undefined>
}

/** One user of `GET /databases/{id}/redis/acl`. */
export type RedisACLUser = {
  name: string
  enabled: boolean
  /** It accepts any password. */
  noPassword: boolean
  /** How many passwords are set. The hashes are never sent. */
  passwords: number
  keys: string[] | null
  channels: string[] | null
  /** In order: `+@all`, `-@dangerous`. */
  commands: string[] | null
  selectors?: string[]
  flags?: string[]
  /** Every command on every key. */
  unrestricted: boolean
  /** The default user. */
  system: boolean
  /** The account the dashboard connects as. */
  self: boolean
  /** The whole rule, one line, without passwords. */
  rule: string
}

export type RedisACL = { supported: boolean; users: RedisACLUser[]; reason?: string }

export type RedisACLRequest = {
  create?: boolean
  enabled?: boolean
  password?: string
  noPassword?: boolean
  keys?: string[]
  channels?: string[]
  commands?: string[]
}

export type RedisACLChange = { user: RedisACLUser; persisted: boolean; notice?: string }

export type MongoRoleRef = { role: string; db: string }

export type MongoUser = {
  user: string
  /** The database the account lives in. */
  db: string
  roles: MongoRoleRef[]
  mechanisms: string[]
  /** It holds root. */
  superuser: boolean
  /** The account the dashboard is connected as. */
  self: boolean
}

export type MongoUsers = { users: MongoUser[] }

export type MongoRole = {
  role: string
  db: string
  builtin: boolean
  /** Inherited roles. */
  roles: MongoRoleRef[]
  /** A custom role's own privileges. */
  privileges: { resource: string; actions: string[] }[]
}

export type MongoRoles = { database: string; roles: MongoRole[] }

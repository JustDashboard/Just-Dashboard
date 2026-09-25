"use client"

import { useId, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Copy, Key, Plus, Puzzle, Trash, Users } from "@/components/icons"
import { del, errorMessage, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { bytes, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DbConnection,
  DbDriverInfo,
  DbExtensions,
  DbGrantLevel,
  DbRole,
  DbRoles,
  DbSettings,
} from "@/lib/types"
import type { Database as DbCatalogue } from "@/components/database/server-types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { ChipStrip, FilterChip, ChipCount } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { VerbActions, type Verb } from "@/components/verbs"
import { Field, FieldRow, OptionList, OptionRow, Statement } from "@/components/form"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/**
 * The server behind the connection: the databases on it, the accounts that
 * sign in, the extensions it offers and the parameters it was started with.
 *
 * Every hosted platform has this page — Supabase calls its halves Roles and
 * Extensions — and it is the one an operator on their own server needs most,
 * because the alternative is `CREATE ROLE` syntax remembered for six engines.
 * The forms show the shape of what they will do, and a new account can be
 * handed a database in the same breath: "an account for this app, with write
 * access to its database" is one dialog rather than three statements.
 */
export function ServerTab({
  conn,
  info,
  confirm,
  onConnected,
}: {
  conn: DbConnection
  info?: DbDriverInfo
  confirm: ConfirmFn
  /** A sibling connection was saved: land on it. */
  onConnected: (id: number) => void
}) {
  const [view, setView] = useSessionState<"databases" | "roles" | "extensions" | "settings">(
    `databases.${conn.id}.server.view`,
    "databases",
  )
  const { can } = useAuth()
  const admin = can("system.admin")
  const roles = usePoll(
    (signal) => get<DbRoles>(`/databases/${conn.id}/server/roles`, undefined, signal),
    30_000,
    [conn.id],
  )
  const catalogue = usePoll(
    (signal) => get<DbCatalogue[]>(`/databases/${conn.id}/schemas`, undefined, signal),
    60_000,
    [conn.id],
  )
  const extensions = usePoll(
    (signal) => get<DbExtensions>(`/databases/${conn.id}/server/extensions`, undefined, signal),
    0,
    [conn.id],
    { enabled: conn.driver !== "redis" && conn.driver !== "mongodb" && conn.driver !== "sqlite" },
  )
  const settings = usePoll(
    (signal) => get<DbSettings>(`/databases/${conn.id}/server/settings`, undefined, signal),
    60_000,
    [conn.id],
  )

  if (conn.driver === "sqlite") {
    return (
      <EmptyState
        icon={Users}
        title="A SQLite database has no server"
        description="It is one file with no accounts, no extensions catalogue and no parameters to read. Everything about it is under Browse and Structure."
      />
    )
  }

  const roleCount = roles.data?.roles.length
  const installed = extensions.data?.extensions.filter((e) => e.installed).length

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <ChipStrip>
        <FilterChip selected={view === "databases"} onClick={() => setView("databases")}>
          Databases
          {catalogue.data && <ChipCount>{catalogue.data.length}</ChipCount>}
        </FilterChip>
        <FilterChip selected={view === "roles"} onClick={() => setView("roles")}>
          Accounts
          {roleCount !== undefined && <ChipCount>{roleCount}</ChipCount>}
        </FilterChip>
        {extensions.data?.supported && (
          <FilterChip selected={view === "extensions"} onClick={() => setView("extensions")}>
            Extensions
            {installed !== undefined && <ChipCount>{installed}</ChipCount>}
          </FilterChip>
        )}
        <FilterChip selected={view === "settings"} onClick={() => setView("settings")}>
          Settings
        </FilterChip>
      </ChipStrip>

      {view === "databases" && (
        <DatabasesPanel
          conn={conn}
          info={info}
          admin={admin}
          catalogue={catalogue}
          roles={roles.data?.roles ?? []}
          onConnected={onConnected}
        />
      )}
      {view === "roles" && (
        <RolesPanel
          conn={conn}
          admin={admin}
          confirm={confirm}
          roles={roles}
          catalogue={catalogue.data ?? []}
        />
      )}
      {view === "extensions" && (
        <ExtensionsPanel conn={conn} admin={admin} confirm={confirm} extensions={extensions} />
      )}
      {view === "settings" && <SettingsPanel settings={settings} />}
    </div>
  )
}

// --- databases on the server -------------------------------------------------

function DatabasesPanel({
  conn,
  info,
  admin,
  catalogue,
  roles,
  onConnected,
}: {
  conn: DbConnection
  info?: DbDriverInfo
  admin: boolean
  catalogue: ReturnType<typeof usePoll<DbCatalogue[]>>
  roles: DbRole[]
  onConnected: (id: number) => void
}) {
  const [creating, setCreating] = useState(false)
  const [connecting, setConnecting] = useState<string | null>(null)
  const word = info?.kind === "keyvalue" ? "keyspace" : "database"
  const canCreate = admin && conn.driver !== "redis" && conn.driver !== "oracle"

  const connect = async (name: string) => {
    setConnecting(name)
    try {
      const saved = await post<DbConnection>(`/databases/${conn.id}/server/databases/connect`, {
        database: name,
      })
      notify.success(`Connected ${saved.name}`)
      onConnected(saved.id)
    } catch (err) {
      notify.error(`Could not connect to ${name}`, err)
    } finally {
      setConnecting(null)
    }
  }

  if (catalogue.loading && !catalogue.data) return <LoadingPanel />
  if (catalogue.error) return <ErrorState error={catalogue.error} />
  const list = catalogue.data ?? []
  const total = list.reduce((sum, d) => sum + (d.size ?? 0), 0)

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title={`${plural(list.length, word)} on this server`}
        actions={
          <>
            {total > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {bytes(total)} together
              </span>
            )}
            {canCreate && (
              <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                <Plus className="size-3.5" />
                Create {word}
              </Button>
            )}
          </>
        }
      />
      <RowList>
        {list.map((d) => {
          const current = d.name === conn.database
          return (
            <Row
              key={d.name}
              title={
                <span className="flex items-center gap-2">
                  <span className="font-mono">{d.name}</span>
                  {current && <Tag>this connection</Tag>}
                </span>
              }
              subtitle={[
                d.size ? bytes(d.size) : null,
                d.owner ? `owned by ${d.owner}` : null,
                d.encoding || null,
              ]
                .filter(Boolean)
                .join(" · ")}
              trailing={
                !current &&
                admin &&
                conn.driver !== "redis" && (
                  <Button
                    size="sm"
                    variant="outline"
                    pending={connecting === d.name}
                    onClick={() => void connect(d.name)}
                  >
                    Open as a connection
                  </Button>
                )
              }
            />
          )
        })}
      </RowList>
      {creating && (
        <CreateDatabaseDialog
          conn={conn}
          roles={roles}
          onOpenChange={setCreating}
          onDone={(saved) => {
            catalogue.refresh()
            if (saved) onConnected(saved.id)
          }}
        />
      )}
    </Panel>
  )
}

function CreateDatabaseDialog({
  conn,
  roles,
  onOpenChange,
  onDone,
}: {
  conn: DbConnection
  roles: DbRole[]
  onOpenChange: (open: boolean) => void
  onDone: (saved: DbConnection | null) => void
}) {
  const id = useId()
  const [name, setName] = useState("")
  const [owner, setOwner] = useState("")
  const [connect, setConnect] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const owners = roles.filter((r) => r.login && !r.system)
  const valid = /^[A-Za-z_][A-Za-z0-9_]*$/.test(name)
  const statement = valid
    ? conn.driver === "postgres"
      ? `CREATE DATABASE "${name}"${owner ? ` OWNER "${owner}"` : ""};`
      : conn.driver === "mongodb"
        ? `use ${name}; db.createCollection("_init")`
        : `CREATE DATABASE ${name};`
    : ""

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await post<DbConnection | { ok: true }>(
        `/databases/${conn.id}/server/databases`,
        {
          name,
          owner: owner || undefined,
          connect,
        },
      )
      notify.success(`Created ${name}`)
      onOpenChange(false)
      onDone("id" in res ? res : null)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title={`Create ${conn.driver === "redis" ? "keyspace" : "database"}`}
      description="Creates an empty database on this server."
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!valid || busy} pending={busy} onClick={create}>
            Create
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {error && (
          <Notice title="The server refused" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <FieldRow>
          <Field label="Name" htmlFor={`${id}-name`} hint="Letters, digits and underscores.">
            <Input
              id={`${id}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="font-mono"
              autoFocus
              autoComplete="off"
            />
          </Field>
          {conn.driver === "postgres" && (
            <Field label="Owner" htmlFor={`${id}-owner`} hint="The account that owns it.">
              <Select
                value={owner || "__self"}
                onValueChange={(v) => setOwner(v === "__self" ? "" : v)}
              >
                <SelectTrigger id={`${id}-owner`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__self">{conn.user || "this connection's account"}</SelectItem>
                  {owners
                    .filter((r) => r.name !== conn.user)
                    .map((r) => (
                      <SelectItem key={r.name} value={r.name}>
                        {r.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </Field>
          )}
        </FieldRow>
        <OptionList>
          <OptionRow
            title="Connect it now"
            hint="Saves a connection to the new database under the same credentials and opens it."
            checked={connect}
            onCheckedChange={setConnect}
          />
        </OptionList>
        <Statement sql={statement} placeholder="Name the database." />
      </div>
    </Modal>
  )
}

// --- accounts ----------------------------------------------------------------

function RolesPanel({
  conn,
  admin,
  confirm,
  roles,
  catalogue,
}: {
  conn: DbConnection
  admin: boolean
  confirm: ConfirmFn
  roles: ReturnType<typeof usePoll<DbRoles>>
  catalogue: DbCatalogue[]
}) {
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<{ role: DbRole; mode: "password" | "grant" } | null>(null)
  const [query, setQuery] = useState("")

  if (roles.loading && !roles.data) return <LoadingPanel />
  if (roles.error) return <ErrorState error={roles.error} />
  if (!roles.data) return null
  if (!roles.data.supported) {
    return (
      <Notice title="No account list on this server" icon={Users}>
        {roles.data.reason}
      </Notice>
    )
  }
  const mysql = conn.driver === "mysql"
  const list = roles.data.roles.filter((r) =>
    `${r.name}@${r.host ?? ""}`.toLowerCase().includes(query.trim().toLowerCase()),
  )

  const drop = (role: DbRole) =>
    confirm({
      title: "Drop account",
      confirmLabel: "Drop account",
      description: (
        <p>
          Removes{" "}
          <b>
            {role.name}
            {role.host ? `@${role.host}` : ""}
          </b>{" "}
          from the server. Anything signing in as it stops working now; the engine refuses if the
          account still owns objects.
        </p>
      ),
      action: async (c) => {
        await del(
          `/databases/${conn.id}/server/roles/${encodeURIComponent(role.name)}${role.host ? `?host=${encodeURIComponent(role.host)}` : ""}`,
          { confirm: c },
        )
        notify.success(`Dropped ${role.name}`)
        roles.refresh()
      },
    })

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Accounts"
        actions={
          <>
            <SearchInput
              dense
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter accounts…"
              aria-label="Filter accounts"
            />
            {admin && (
              <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                <Plus className="size-3.5" />
                New account
              </Button>
            )}
          </>
        }
      />
      <PanelBody flush>
        <div className="min-w-0 overflow-x-auto group-data-[plain]/panel:-mx-4">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Account</TableHead>
                <TableHead>Can</TableHead>
                <TableHead className="w-28 text-right">Sessions</TableHead>
                <TableHead className="w-28 text-right">Limit</TableHead>
                {admin && <TableHead className="w-24" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((role) => {
                const verbs: Verb[] = admin
                  ? [
                      {
                        key: "password",
                        label: "Change password",
                        icon: Key,
                        run: () => setEditing({ role, mode: "password" }),
                      },
                      ...(conn.driver !== "redis"
                        ? [
                            {
                              key: "grant",
                              label: "Grant a database",
                              icon: Plus,
                              run: () => setEditing({ role, mode: "grant" }),
                            },
                          ]
                        : []),
                      {
                        key: "drop",
                        label: "Drop account",
                        icon: Trash,
                        danger: true,
                        disabled: role.system || role.name === conn.user,
                        run: () => drop(role),
                      },
                    ]
                  : []
                return (
                  <TableRow key={`${role.name}@${role.host ?? ""}`}>
                    <TableCell>
                      <span className="flex min-w-0 flex-wrap items-center gap-2">
                        <span className="font-mono">
                          {role.name}
                          {mysql && role.host ? (
                            <span className="text-muted-foreground">@{role.host}</span>
                          ) : null}
                        </span>
                        {role.name === conn.user && <Tag>this connection</Tag>}
                        {role.system && <Tag>system</Tag>}
                        {role.locked && <Tag tone="warning">locked</Tag>}
                        {!role.login && !role.locked && <Tag>no login</Tag>}
                      </span>
                      {role.memberOf && role.memberOf.length > 0 && (
                        <span className="block truncate text-hint text-muted-foreground">
                          {role.memberOf.join(", ")}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {[
                        role.superuser ? "everything" : null,
                        !role.superuser && role.createDb ? "create databases" : null,
                        !role.superuser && role.createRole ? "create accounts" : null,
                      ]
                        .filter(Boolean)
                        .join(", ") || (role.login ? "sign in" : "—")}
                    </TableCell>
                    <TableCell className="numeric text-right">{role.connections}</TableCell>
                    <TableCell className="numeric text-right text-muted-foreground">
                      {role.connectionLimit < 0 ? "none" : role.connectionLimit}
                    </TableCell>
                    {admin && (
                      <TableCell className="text-right">
                        <VerbActions verbs={verbs} dim menuLabel={`Actions for ${role.name}`} />
                      </TableCell>
                    )}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </PanelBody>
      {creating && (
        <RoleDialog
          conn={conn}
          catalogue={catalogue}
          onOpenChange={setCreating}
          onDone={roles.refresh}
        />
      )}
      {editing?.mode === "password" && (
        <PasswordDialog
          conn={conn}
          role={editing.role}
          onOpenChange={(o) => !o && setEditing(null)}
          onDone={roles.refresh}
        />
      )}
      {editing?.mode === "grant" && (
        <GrantDialog
          conn={conn}
          role={editing.role}
          catalogue={catalogue}
          onOpenChange={(o) => !o && setEditing(null)}
          onDone={roles.refresh}
        />
      )}
    </Panel>
  )
}

/** A password nobody has to invent: 24 bytes, URL-safe, so it pastes into a DSN. */
export function suggestPassword() {
  const buf = new Uint8Array(24)
  crypto.getRandomValues(buf)
  return btoa(String.fromCharCode(...buf))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "")
}

const LEVEL_WORD: Record<DbGrantLevel, string> = {
  read: "read only",
  write: "read and write",
  all: "everything",
}

function grantStatement(
  conn: DbConnection,
  role: string,
  host: string,
  database: string,
  level: DbGrantLevel,
) {
  switch (conn.driver) {
    case "postgres":
      return level === "all"
        ? `GRANT ALL PRIVILEGES ON DATABASE "${database}" TO "${role}";\nGRANT ALL ON SCHEMA public TO "${role}";\nGRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO "${role}";\nALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO "${role}";`
        : level === "write"
          ? `GRANT CONNECT ON DATABASE "${database}" TO "${role}";\nGRANT USAGE ON SCHEMA public TO "${role}";\nGRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "${role}";\nALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "${role}";`
          : `GRANT CONNECT ON DATABASE "${database}" TO "${role}";\nGRANT USAGE ON SCHEMA public TO "${role}";\nGRANT SELECT ON ALL TABLES IN SCHEMA public TO "${role}";\nALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "${role}";`
    case "mysql":
      return `GRANT ${level === "all" ? "ALL PRIVILEGES" : level === "write" ? "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW" : "SELECT, SHOW VIEW"} ON \`${database}\`.* TO '${role}'@'${host || "%"}';`
    case "mongodb":
      return `db.getSiblingDB("admin").grantRolesToUser("${role}", [{ role: "${level === "all" ? "dbOwner" : level === "write" ? "readWrite" : "read"}", db: "${database}" }])`
    case "sqlserver":
      return `USE [${database}];\nCREATE USER [${role}] FOR LOGIN [${role}];\nALTER ROLE ${level === "all" ? "db_owner" : level === "write" ? "db_datawriter" : "db_datareader"} ADD MEMBER [${role}];`
    case "clickhouse":
      return `GRANT ${level === "all" ? "ALL" : level === "write" ? "SELECT, INSERT, ALTER DELETE, ALTER UPDATE" : "SELECT"} ON ${database}.* TO ${role};`
  }
  return ""
}

function createStatement(conn: DbConnection, name: string, host: string, superuser: boolean) {
  const pw = "'••••••••'"
  switch (conn.driver) {
    case "postgres":
      return `CREATE ROLE "${name}" WITH LOGIN${superuser ? " SUPERUSER CREATEDB CREATEROLE" : ""} PASSWORD ${pw};`
    case "mysql":
      return `CREATE USER '${name}'@'${host || "%"}' IDENTIFIED BY ${pw};${superuser ? `\nGRANT ALL PRIVILEGES ON *.* TO '${name}'@'${host || "%"}' WITH GRANT OPTION;` : ""}`
    case "mongodb":
      return `db.getSiblingDB("admin").createUser({ user: "${name}", pwd: ${pw}, roles: [${superuser ? `{ role: "root", db: "admin" }` : ""}] })`
    case "redis":
      return `ACL SETUSER ${name} on >•••••••• ~* &* ${superuser ? "+@all" : "+@all -@dangerous"}`
    case "sqlserver":
      return `CREATE LOGIN [${name}] WITH PASSWORD = ${pw};${superuser ? `\nALTER SERVER ROLE sysadmin ADD MEMBER [${name}];` : ""}`
    case "clickhouse":
      return `CREATE USER ${name} IDENTIFIED WITH sha256_password BY ${pw};${superuser ? `\nGRANT ALL ON *.* TO ${name} WITH GRANT OPTION;` : ""}`
  }
  return ""
}

function RoleDialog({
  conn,
  catalogue,
  onOpenChange,
  onDone,
}: {
  conn: DbConnection
  catalogue: DbCatalogue[]
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const id = useId()
  const [name, setName] = useState("")
  const [host, setHost] = useState("%")
  const [password, setPassword] = useState(suggestPassword)
  const [superuser, setSuperuser] = useState(false)
  const [grant, setGrant] = useState(conn.driver !== "redis" && Boolean(conn.database))
  const [database, setDatabase] = useState(conn.database || catalogue[0]?.name || "")
  const [level, setLevel] = useState<DbGrantLevel>("write")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const mysql = conn.driver === "mysql"
  const valid = /^[A-Za-z_][A-Za-z0-9_.-]*$/.test(name) && password.length >= 8
  const statement = valid
    ? [
        createStatement(conn, name, host, superuser),
        grant && database && !superuser ? grantStatement(conn, name, host, database, level) : "",
      ]
        .filter(Boolean)
        .join("\n")
    : ""

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post(`/databases/${conn.id}/server/roles`, {
        name,
        host: mysql ? host : undefined,
        password,
        superuser,
        database: grant && !superuser ? database : undefined,
        level: grant && !superuser ? level : undefined,
      })
      notify.success(`Created ${name}`, {
        description: "The password is not stored here — copy it now if you have not.",
      })
      onOpenChange(false)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="lg"
      title="New account"
      description="Creates an account on the server, optionally with access to one database."
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!valid || busy} pending={busy} onClick={create}>
            <Key />
            Create account
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {error && (
          <Notice title="The server refused" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <FieldRow>
          <Field label="Name" htmlFor={`${id}-name`} hint="Letters, digits and underscores.">
            <Input
              id={`${id}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="font-mono"
              autoFocus
              autoComplete="off"
              placeholder="app"
            />
          </Field>
          {mysql && (
            <Field
              label="From host"
              htmlFor={`${id}-host`}
              hint="% is anywhere; localhost is this machine only."
            >
              <Input
                id={`${id}-host`}
                value={host}
                onChange={(e) => setHost(e.target.value)}
                className="font-mono"
              />
            </Field>
          )}
        </FieldRow>
        <Field
          label="Password"
          htmlFor={`${id}-password`}
          hint="Generated here and never stored by the dashboard. Copy it for the application that will use it."
          trailing={
            <span className="flex items-center gap-1">
              <Button size="xs" variant="ghost" onClick={() => setPassword(suggestPassword())}>
                Generate
              </Button>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => void copyText(password, "Password copied")}
              >
                <Copy />
                Copy
              </Button>
            </span>
          }
        >
          <Input
            id={`${id}-password`}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="font-mono"
            autoComplete="new-password"
          />
        </Field>
        <OptionList>
          <OptionRow
            title="Administrator of the whole server"
            hint="Every database, every account, every setting. For an application, leave this off and grant it its own database below."
            checked={superuser}
            onCheckedChange={setSuperuser}
            tone="warning"
          />
          {conn.driver !== "redis" && (
            <OptionRow
              title="Give it a database"
              hint="Grants access to one database now, so the account is usable the moment it exists."
              checked={grant && !superuser}
              onCheckedChange={setGrant}
              disabled={superuser}
            >
              <FieldRow>
                <Field label="Database" htmlFor={`${id}-db`}>
                  <Select value={database} onValueChange={setDatabase}>
                    <SelectTrigger id={`${id}-db`} className="w-full">
                      <SelectValue placeholder="Pick one" />
                    </SelectTrigger>
                    <SelectContent>
                      {(catalogue.length > 0
                        ? catalogue
                        : conn.database
                          ? [{ name: conn.database }]
                          : []
                      ).map((d) => (
                        <SelectItem key={d.name} value={d.name}>
                          {d.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field label="Access" htmlFor={`${id}-level`}>
                  <Select value={level} onValueChange={(v) => setLevel(v as DbGrantLevel)}>
                    <SelectTrigger id={`${id}-level`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="read">Read only</SelectItem>
                      <SelectItem value="write">Read and write</SelectItem>
                      <SelectItem value="all">Everything on it</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
              </FieldRow>
            </OptionRow>
          )}
        </OptionList>
        <Statement
          sql={statement}
          placeholder="Name the account and give it a password of eight characters or more."
        />
      </div>
    </Modal>
  )
}

function PasswordDialog({
  conn,
  role,
  onOpenChange,
  onDone,
}: {
  conn: DbConnection
  role: DbRole
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const id = useId()
  const [password, setPassword] = useState(suggestPassword)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const self = role.name === conn.user

  const save = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await put(`/databases/${conn.id}/server/roles/${encodeURIComponent(role.name)}`, {
        password,
        host: role.host,
      })
      notify.success(`Changed the password for ${role.name}`)
      onOpenChange(false)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title={`Change password for ${role.name}`}
      description="Sets a new password on the server."
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={password.length < 8 || busy} pending={busy} onClick={save}>
            Change password
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {error && (
          <Notice title="The server refused" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        {self && (
          <Notice title="This is the account the dashboard signs in with" tone="warning">
            After the change, edit this connection under Connection with the new password, or the
            dashboard is locked out of its own database.
          </Notice>
        )}
        <Field
          label="New password"
          htmlFor={`${id}-password`}
          hint="Whatever signs in as this account has to be told. The dashboard does not keep it."
          trailing={
            <span className="flex items-center gap-1">
              <Button size="xs" variant="ghost" onClick={() => setPassword(suggestPassword())}>
                Generate
              </Button>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => void copyText(password, "Password copied")}
              >
                <Copy />
                Copy
              </Button>
            </span>
          }
        >
          <Input
            id={`${id}-password`}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="font-mono"
            autoFocus
            autoComplete="new-password"
          />
        </Field>
      </div>
    </Modal>
  )
}

function GrantDialog({
  conn,
  role,
  catalogue,
  onOpenChange,
  onDone,
}: {
  conn: DbConnection
  role: DbRole
  catalogue: DbCatalogue[]
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const id = useId()
  const [database, setDatabase] = useState(conn.database || catalogue[0]?.name || "")
  const [level, setLevel] = useState<DbGrantLevel>("write")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const save = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post(`/databases/${conn.id}/server/roles/${encodeURIComponent(role.name)}/grant`, {
        database,
        level,
        host: role.host,
      })
      notify.success(`${role.name} can now ${LEVEL_WORD[level]} on ${database}`)
      onOpenChange(false)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="lg"
      title={`Grant ${role.name} a database`}
      description="Hands the account a level of access to one database."
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!database || busy} pending={busy} onClick={save}>
            Grant
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {error && (
          <Notice title="The server refused" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <FieldRow>
          <Field label="Database" htmlFor={`${id}-db`}>
            <Select value={database} onValueChange={setDatabase}>
              <SelectTrigger id={`${id}-db`} className="w-full">
                <SelectValue placeholder="Pick one" />
              </SelectTrigger>
              <SelectContent>
                {(catalogue.length > 0
                  ? catalogue
                  : conn.database
                    ? [{ name: conn.database }]
                    : []
                ).map((d) => (
                  <SelectItem key={d.name} value={d.name}>
                    {d.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Access" htmlFor={`${id}-level`}>
            <Select value={level} onValueChange={(v) => setLevel(v as DbGrantLevel)}>
              <SelectTrigger id={`${id}-level`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="read">Read only</SelectItem>
                <SelectItem value="write">Read and write</SelectItem>
                <SelectItem value="all">Everything on it</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        <Statement
          sql={database ? grantStatement(conn, role.name, role.host ?? "", database, level) : ""}
          placeholder="Pick a database."
        />
      </div>
    </Modal>
  )
}

// --- extensions --------------------------------------------------------------

function ExtensionsPanel({
  conn,
  admin,
  confirm,
  extensions,
}: {
  conn: DbConnection
  admin: boolean
  confirm: ConfirmFn
  extensions: ReturnType<typeof usePoll<DbExtensions>>
}) {
  const [query, setQuery] = useState("")
  const [busy, setBusy] = useState<string | null>(null)
  if (extensions.loading && !extensions.data) return <LoadingPanel />
  if (extensions.error) return <ErrorState error={extensions.error} />
  if (!extensions.data) return null
  if (!extensions.data.supported) {
    return (
      <Notice title="No extension catalogue on this engine" icon={Puzzle}>
        {extensions.data.reason}
      </Notice>
    )
  }
  const editable = admin && extensions.data.editable
  const list = extensions.data.extensions.filter((e) =>
    `${e.name} ${e.comment ?? ""}`.toLowerCase().includes(query.trim().toLowerCase()),
  )
  const installed = list.filter((e) => e.installed)
  const available = list.filter((e) => !e.installed)

  const enable = async (name: string) => {
    setBusy(name)
    try {
      await post(`/databases/${conn.id}/server/extensions`, { name })
      notify.success(`Enabled ${name}`)
      extensions.refresh()
    } catch (err) {
      notify.error(`Could not enable ${name}`, err)
    } finally {
      setBusy(null)
    }
  }
  const disable = (name: string) =>
    confirm({
      title: "Disable extension",
      confirmLabel: "Disable",
      description: (
        <p>
          Drops <b>{name}</b> from this database. Any column, index or function that depends on it
          makes the engine refuse.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/server/extensions/${encodeURIComponent(name)}`, {
          confirm: c,
        })
        notify.success(`Disabled ${name}`)
        extensions.refresh()
      },
    })

  const rows = (items: typeof list) => (
    <RowList>
      {items.map((e) => (
        <Row
          key={e.name}
          title={
            <span className="flex items-center gap-2">
              <span className="font-mono">{e.name}</span>
              {e.version && (
                <span className="numeric text-hint text-muted-foreground">{e.version}</span>
              )}
              {e.installed && e.availableVersion && e.availableVersion !== e.version && (
                <Tag tone="warning">{e.availableVersion} available</Tag>
              )}
            </span>
          }
          subtitle={e.comment || (e.schema ? `in ${e.schema}` : undefined)}
          trailing={
            editable ? (
              e.installed ? (
                <Button size="sm" variant="ghost" onClick={() => disable(e.name)}>
                  Disable
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  pending={busy === e.name}
                  onClick={() => void enable(e.name)}
                >
                  Enable
                </Button>
              )
            ) : e.installed ? (
              <Tag tone="success">{conn.driver === "mysql" ? "active" : "enabled"}</Tag>
            ) : undefined
          }
        />
      ))}
    </RowList>
  )

  return (
    <div className="flex min-w-0 animate-rise flex-col gap-6">
      <Panel plain>
        <PanelHeader
          title={`${plural(installed.length, conn.driver === "mysql" ? "plugin" : "extension")} enabled`}
          actions={
            <SearchInput
              dense
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter…"
              aria-label="Filter extensions"
            />
          }
        />
        {installed.length > 0 ? (
          rows(installed)
        ) : (
          <p className="text-hint text-muted-foreground">None enabled.</p>
        )}
      </Panel>
      {available.length > 0 && (
        <Panel plain>
          <PanelHeader title={`${available.length} available`} />
          {rows(available)}
        </Panel>
      )}
    </div>
  )
}

// --- settings ----------------------------------------------------------------

function SettingsPanel({ settings }: { settings: ReturnType<typeof usePoll<DbSettings>> }) {
  const [query, setQuery] = useState("")
  if (settings.loading && !settings.data) return <LoadingPanel />
  if (settings.error) return <ErrorState error={settings.error} />
  if (!settings.data) return null
  const list = settings.data.settings.filter((s) =>
    `${s.name} ${s.value}`.toLowerCase().includes(query.trim().toLowerCase()),
  )
  const text = list.map((s) => `${s.name} = ${s.value}${s.unit ? ` ${s.unit}` : ""}`).join("\n")
  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Server parameters"
        actions={
          <>
            <SearchInput
              dense
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter…"
              aria-label="Filter settings"
            />
            <Button
              size="sm"
              variant="ghost"
              onClick={() => void copyText(text, "Settings copied")}
            >
              <Copy className="size-3.5" />
              Copy all
            </Button>
          </>
        }
      />
      <PanelBody flush>
        <div className="min-w-0 overflow-x-auto group-data-[plain]/panel:-mx-4">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-72">Parameter</TableHead>
                <TableHead className="w-56">Value</TableHead>
                <TableHead>Means</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((s) => (
                <TableRow key={s.name}>
                  <TableCell className="font-mono">{s.name}</TableCell>
                  <TableCell className="font-mono">
                    {s.value}
                    {s.unit && <span className="text-muted-foreground"> {s.unit}</span>}
                  </TableCell>
                  <TableCell className={cn("text-muted-foreground", !s.description && "text-hint")}>
                    {s.description || s.category || ""}
                    {s.restartRequired && <Tag className="ml-2">restart to change</Tag>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </PanelBody>
    </Panel>
  )
}

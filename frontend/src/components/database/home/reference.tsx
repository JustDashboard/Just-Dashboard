"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { Archive, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { Container, DbAccess, DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { OutcomeStrip, type Outcome } from "@/components/outcome-strip"
import { Detail, DetailList } from "@/components/page"
import { ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { sectionHref } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit"
import {
  Block,
  BlockLink,
  CardsSkeleton,
  FactsSkeleton,
  Quiet,
  Read,
  staleOf,
} from "@/components/database/home/blocks"
import { nameHue } from "@/components/database/home/kinds"
import { read, record } from "@/components/database/home/read"
import { compact } from "@/components/database/home/readings"
import type {
  DbBackups,
  DbServerDatabase,
  DbTransferJob,
  RedisServer,
} from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

/**
 * The reference blocks at the foot of a home: what the server runs as, where
 * it is reachable from, its dumps, and what else is on the same server. Each
 * is a short list of facts with the one way to the page that changes them.
 * None of them dials the database, so a stopped one keeps them all.
 */

/** What the server runs as: its container, its unit, its file, or another machine. */
export function RunsAs() {
  const { conn, summary, engine, href } = useDatabase()
  const inspected = summary?.container?.id
  // What the container was told it may use is Docker's to say, not the
  // summary's: asked of the container's own record, and only for a database
  // that runs in one.
  const limits = usePoll(
    (signal) =>
      read<Container>(
        `/docker/containers/${encodeURIComponent(inspected ?? "")}`,
        (answer) => typeof answer.id === "string",
        undefined,
        signal,
      ),
    60_000,
    [inspected],
    { enabled: Boolean(inspected) },
  )
  if (!summary) return null
  const container = summary.container
  const unit = summary.unit
  const file = summary.file
  const logs = engine.has("logs")
  const directory = file ? file.path.replace(/\/[^/]*$/, "") || "/" : ""

  return (
    <Block
      title="Runs as"
      actions={
        <>
          {container && (
            <BlockLink href={`/docker/containers/${encodeURIComponent(container.name)}`}>
              Open container
            </BlockLink>
          )}
          {!container && unit && (
            <BlockLink href={`/processes/services?unit=${encodeURIComponent(unit.name)}`}>
              Open unit
            </BlockLink>
          )}
          {file && (
            <BlockLink href={`/files?path=${encodeURIComponent(directory)}`}>Open folder</BlockLink>
          )}
          {logs && <BlockLink href={href("logs")}>Logs</BlockLink>}
        </>
      }
    >
      <DetailList className="animate-rise">
        {container && (
          <>
            <Detail label="Container" className="font-mono wrap-anywhere">
              {container.name}
            </Detail>
            <Detail label="Image" className="font-mono wrap-anywhere">
              {container.image}
            </Detail>
            <Detail label="Docker says">
              {container.status ?? container.state}
              {container.health ? ` · ${container.health}` : ""}
            </Detail>
            {container.composeProject && (
              <Detail label="Compose" className="font-mono wrap-anywhere">
                {container.composeProject}
                {container.composeService ? ` / ${container.composeService}` : ""}
              </Detail>
            )}
            <Detail label="Limits">
              <ContainerLimits poll={limits} />
            </Detail>
            {limits.data?.restartPolicy && (
              <Detail label="Restarts" className="font-mono">
                {limits.data.restartPolicy}
              </Detail>
            )}
          </>
        )}
        {!container && unit && (
          <>
            <Detail label="Unit" className="font-mono wrap-anywhere">
              {unit.name}
            </Detail>
            <Detail label="systemd says">
              {unit.activeState}
              {unit.subState ? ` (${unit.subState})` : ""}
            </Detail>
          </>
        )}
        {file && (
          <>
            <Detail label="File" className="font-mono wrap-anywhere">
              {file.path}
            </Detail>
            <Detail label="On disk">
              {file.exists ? (
                <>
                  {bytes(file.size)}
                  {file.modified ? (
                    <span title={timestamp(file.modified)}>
                      {" "}
                      · written {relativeTime(file.modified)}
                    </span>
                  ) : null}
                </>
              ) : (
                <span className="flex">
                  <Status tone="danger" label="the file is missing" />
                </span>
              )}
            </Detail>
          </>
        )}
        {!container && !unit && !file && (
          <Detail label="Where">
            {summary.source === "remote"
              ? `On another machine, at ${conn.host}${conn.port ? `:${conn.port}` : ""}`
              : summary.source === "host"
                ? `A process on this server, listening on port ${conn.port}`
                : "Not known"}
          </Detail>
        )}
        <Detail label="Start and stop">
          {summary.power.via === "docker"
            ? "From here, through Docker"
            : summary.power.via === "systemd"
              ? "From here, through systemd"
              : (summary.power.reason ?? "Not from here")}
        </Detail>
      </DetailList>
    </Block>
  )
}

/**
 * What the container may use of the machine. Docker inspects only a running
 * container for its limits, so one that is not says that rather than "none"
 * — which is an answer, and a different one.
 */
function ContainerLimits({ poll }: { poll: PollState<Container> }) {
  const data = poll.data
  if (!data) {
    return poll.error ? (
      <>
        <span className="text-muted-foreground">Could not be read: {poll.error.message}</span>{" "}
        <button type="button" className="rounded-sm underline focus-ring" onClick={poll.refresh}>
          Try again
        </button>
      </>
    ) : (
      <Skeleton className="my-0.5 h-3 w-32" />
    )
  }
  if (!data.inspected) {
    return <span className="text-muted-foreground">Not read while the container is down</span>
  }
  const memory = data.memoryLimit ? `${bytes(data.memoryLimit, 0)} of memory` : undefined
  const cpu = data.cpuLimit
    ? `${Number(data.cpuLimit.toFixed(2))} ${data.cpuLimit === 1 ? "core" : "cores"}`
    : undefined
  if (!memory && !cpu) return "None: it may use all of this machine's memory and processors"
  return (
    <span className="numeric">
      {memory ?? "no memory limit"} · {cpu ?? "no processor quota"}
    </span>
  )
}

const EXPOSURE: Record<string, { word: string; sentence: string }> = {
  local: {
    word: "This server only",
    sentence: "It listens on loopback, so nothing outside this machine can reach it.",
  },
  private: {
    word: "One private address",
    sentence: "It listens on one address of this machine — a private network or a VPN.",
  },
  public: {
    word: "Anywhere",
    sentence: "It listens on every interface, so anything that can reach this machine can try it.",
  },
  remote: {
    word: "Its own machine decides",
    sentence:
      "The server is not on this machine; where it listens is not this dashboard's to read.",
  },
  unknown: { word: "Not known", sentence: "The connection cannot be opened to find out." },
}

/**
 * Where the database is reachable from. Any role reads the summary's word;
 * the addresses and the firewall are an administrator's read, and are asked
 * for only by one.
 */
export function ReachableFrom() {
  const { id, conn, summary, href } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const access = usePoll(
    (signal) =>
      read<DbAccess>(
        `/databases/${id}/access`,
        (answer) => record(answer.firewall) && Array.isArray(answer.publicAddresses),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: admin && Boolean(summary) },
  )
  if (!summary) return null
  const exposure = EXPOSURE[summary.exposure] ?? EXPOSURE.unknown
  const open = summary.exposure === "public"
  const firewall = access.data?.firewall
  return (
    <Block
      title="Reachable from"
      stale={staleOf(access)}
      actions={<BlockLink href={href("settings")}>{admin ? "Change" : "Settings"}</BlockLink>}
    >
      <DetailList className="animate-rise">
        <Detail label="From">
          {open ? (
            <span className="flex">
              <Status verdict="warning" label={exposure.word} />
            </span>
          ) : (
            <span className="font-medium">{exposure.word}</span>
          )}
          <span className="mt-0.5 block text-muted-foreground">{exposure.sentence}</span>
        </Detail>
        {conn.port && (
          <Detail label="Listens on" className="font-mono">
            {conn.host}:{conn.port}
          </Detail>
        )}
        {admin && open && access.data && access.data.publicAddresses.length > 0 && (
          <Detail label="Public address" className="font-mono wrap-anywhere">
            {access.data.publicAddresses.join(", ")}
          </Detail>
        )}
        {admin && firewall && (
          <Detail label="Firewall">
            {firewall.active
              ? `${firewall.backend ?? "The firewall"} is on, and port ${access.data?.port ?? conn.port} is ${firewall.open ? "open" : "closed"}`
              : "No firewall is active on this machine"}
          </Detail>
        )}
        {admin && access.error && !access.data && (
          <Detail label="Firewall">
            <span className="text-muted-foreground">Could not be read: {access.error.message}</span>{" "}
            <button
              type="button"
              className="rounded-sm underline focus-ring"
              onClick={access.refresh}
            >
              Try again
            </button>
          </Detail>
        )}
        <Detail label="Changed">
          {summary.managed
            ? "From Settings: the dashboard owns the binding"
            : summary.source === "remote"
              ? "On the machine it runs on"
              : "In the server's own configuration"}
        </Detail>
      </DetailList>
    </Block>
  )
}

/**
 * The dumps kept of this database, read every thirty seconds — and every two
 * while one is being taken, since a dump in flight is not in the list and the
 * list is the only place its end shows. The read dials no database, so it is
 * made for a stopped server too; the tile above and the block below share it.
 */
export function useBackups(enabled: boolean) {
  const { id } = useDatabase()
  const [watching, setWatching] = useState(false)
  const backups = usePoll(
    async (signal) => {
      const answer = await read<DbBackups>(
        `/databases/${id}/backups`,
        (listed) => Array.isArray(listed.files),
        undefined,
        signal,
      )
      setWatching(Boolean(answer.job))
      return answer
    },
    watching ? 2_000 : 30_000,
    [id],
    { enabled },
  )
  return backups
}

/** How many of the newest dumps the strip draws. */
const STRIP = 14

/**
 * The dumps kept — the newest few as a strip, the newest one's facts — and
 * the way to take one now. A dump runs in the background on the server: the
 * block says it is running in the dump's own words, and reports how it ended
 * when the list stops carrying it.
 */
export function BackupsBlock({
  backups,
  answering = true,
}: {
  backups: PollState<DbBackups>
  /** Whether the server is there to dump. The dumps already taken are listed either way. */
  answering?: boolean
}) {
  const { id, conn, status, href } = useDatabase()
  const { can } = useAuth()
  const [starting, setStarting] = useState(false)
  // The dump this page began, so its end can be told from one begun elsewhere.
  const mine = useRef<string | undefined>(undefined)
  const job = backups.data?.job
  const refresh = backups.refresh
  const check = status.refresh
  const name = conn.name

  useEffect(() => {
    const began = mine.current
    if (!began || !backups.data || backups.data.job) return
    mine.current = undefined
    // The list no longer carries the job: it ended. How, only the job says.
    get<{ job: DbTransferJob }>(`/jobs/${began}`)
      .then(({ job: ended }) => {
        if (ended.status === "succeeded") notify.success(`Backup of ${name} taken`)
        else if (ended.status === "failed") {
          notify.error(`Backup of ${name} failed`, ended.error ?? "The dump did not finish.")
        }
        // A list read before the dump began says nothing about it yet: the
        // next answer is asked the same question.
        else if (ended.status === "running") mine.current = began
      })
      .catch(() => undefined)
      .finally(check)
  }, [backups.data, name, check])

  const start = async () => {
    setStarting(true)
    try {
      const began = await post<DbTransferJob>(`/databases/${id}/backup`, {})
      mine.current = began.id
      refresh()
    } catch (err) {
      notify.error(`Could not back up ${name}`, err)
    } finally {
      setStarting(false)
    }
  }

  const outcomes = useMemo<Outcome[]>(() => {
    const files = backups.data?.files ?? []
    const strip: Outcome[] = files
      .slice(0, STRIP)
      .reverse()
      .map((file) => ({
        key: file.file,
        tone: "success" as const,
        title: `${relativeTime(file.takenAt)} · ${bytes(file.size)} · ${file.tool ?? file.format}`,
      }))
    if (job) strip.push({ key: job.id, tone: "running", title: `${job.title}, running now` })
    return strip
  }, [backups.data, job])

  return (
    <Block
      title="Backups"
      stale={staleOf(backups)}
      actions={<BlockLink href={href("backups")}>All backups</BlockLink>}
    >
      <Read poll={backups} what="the dumps" skeleton={<FactsSkeleton rows={3} />}>
        {(data) => {
          const newest = data.files[0]
          const kept = data.files.reduce((sum, file) => sum + file.size, 0)
          return (
            <div className="space-y-3">
              {data.files.length === 0 && !job ? (
                <Quiet>
                  No dump of {name} has been taken here. A dump is one file in this
                  dashboard&rsquo;s dump directory that Backups can restore from
                  {answering ? "." : "; one can be taken once the server answers."}
                </Quiet>
              ) : (
                <DetailList>
                  <Detail label="Kept">
                    <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <OutcomeStrip
                        items={outcomes}
                        label={`The last ${plural(Math.min(data.files.length, STRIP), "dump")}${job ? ", and one running now" : ""}`}
                      />
                      <span className="numeric">
                        {plural(data.files.length, "dump")} · {bytes(kept)}
                      </span>
                    </span>
                  </Detail>
                  {newest && (
                    <Detail label="Newest">
                      <span title={timestamp(newest.takenAt)}>{relativeTime(newest.takenAt)}</span>
                      {" · "}
                      {bytes(newest.size)}
                      {" · "}
                      {newest.tool ?? newest.format}
                      {newest.summary ? ` · ${newest.summary}` : ""}
                    </Detail>
                  )}
                  <Detail label="Directory" className="font-mono wrap-anywhere">
                    {data.dir}
                  </Detail>
                </DetailList>
              )}
              {job ? (
                <p className="flex text-xs font-medium" data-slot="backup-running">
                  <TextShimmer>{`${job.title}…`}</TextShimmer>
                </p>
              ) : (
                answering &&
                can("service.control") && (
                  <Button size="sm" variant="outline" pending={starting} onClick={start}>
                    <Archive />
                    Back up now
                  </Button>
                )
              )}
            </div>
          )
        }}
      </Read>
    </Block>
  )
}

/** How many of the server's databases are listed before the rest fold away. */
const LISTED = 6

/**
 * A run of cards as many across as the block is wide: three at a desktop's
 * width, two from a tablet's, one on a phone. The cards are `ChoiceRow`s,
 * which bring their own list items.
 */
function CardGrid({ children }: { children: React.ReactNode }) {
  return (
    <ul data-slot="choice-list" className="grid min-w-0 gap-2 sm:grid-cols-2 xl:grid-cols-3">
      {children}
    </ul>
  )
}

/**
 * What else the same server holds. A database that already has a connection
 * is a way to it; one that does not can be given one by an administrator —
 * the same credentials, the other name — and a new one can be made.
 *
 * It runs the page's width as cards (§16): each is a database, drawn as its
 * engine, and the ones that are places to go have the lit edge and the arrow.
 * One with no connection yet keeps its card, quieter, with the verb that
 * gives it one; every card carries the mark, so the names start on one line.
 */
export function ServerDatabases() {
  const { id, conn, engine, readOnly } = useDatabase()
  const { connections, refresh, engineFor } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const [all, setAll] = useState(false)
  const [creating, setCreating] = useState(false)
  const [connecting, setConnecting] = useState<string>()
  const create = useRef<HTMLButtonElement>(null)
  const list = usePoll(
    (signal) =>
      read<DbServerDatabase[]>(
        `/databases/${id}/schemas`,
        (answer) => Array.isArray(answer),
        undefined,
        signal,
      ),
    300_000,
    [id],
  )
  // The catalogue states no "another database can be added to this server".
  // The two things it does state that need one — a restore into a new
  // database, a server the dashboard can start — are together exactly the
  // engines whose route accepts it; the others answer that they hold one
  // database per connection.
  const adds =
    can("system.admin") &&
    !readOnly &&
    (engine.can("restoreNewDatabase") || engine.can("provision"))

  const rows = useMemo(() => {
    const siblings = new Map(
      connections
        .filter(
          (other) =>
            other.driver === conn.driver && other.host === conn.host && other.port === conn.port,
        )
        .map((other) => [other.database, other]),
    )
    return (list.data ?? [])
      .map((database) => ({ database, saved: siblings.get(database.name) }))
      .sort(
        (a, b) =>
          Number(b.database.name === conn.database) - Number(a.database.name === conn.database) ||
          Number(Boolean(b.saved)) - Number(Boolean(a.saved)) ||
          (b.database.size ?? 0) - (a.database.size ?? 0) ||
          a.database.name.localeCompare(b.database.name),
      )
  }, [list.data, connections, conn])

  const open = (saved: DbConnection) => {
    refresh()
    router.push(sectionHref(saved.id))
  }
  const connect = async (database: string) => {
    setConnecting(database)
    try {
      const saved = await post<DbConnection>(`/databases/${id}/server/databases/connect`, {
        database,
      })
      notify.success(`Connected ${saved.name}`)
      open(saved)
    } catch (err) {
      notify.error(`Could not connect to ${database}`, err)
    } finally {
      setConnecting(undefined)
    }
  }
  // The dialog has no trigger of its own to hand the keyboard back to, and
  // left it on the page's body: the button that opened it takes it instead.
  const close = () => {
    setCreating(false)
    requestAnimationFrame(() => create.current?.focus())
  }

  const shown = all ? rows : rows.slice(0, LISTED)
  return (
    <Block
      title="Databases on this server"
      stale={staleOf(list)}
      actions={
        adds && (
          <Button ref={create} size="xs" variant="ghost" onClick={() => setCreating(true)}>
            <Plus />
            New database
          </Button>
        )
      }
    >
      <Read
        poll={list}
        what="the server's databases"
        skeleton={<CardsSkeleton className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3" />}
      >
        {() =>
          rows.length === 0 ? (
            <Quiet>The server lists no database to this account.</Quiet>
          ) : (
            <>
              <CardGrid>
                {shown.map(({ database, saved }) => {
                  const current = database.name === conn.database
                  const goes = saved && !current ? saved : undefined
                  return (
                    <ChoiceRow
                      key={database.name}
                      href={goes ? sectionHref(goes.id) : undefined}
                      disabled={!goes}
                      verb={goes ? `Open ${goes.name}` : database.name}
                      leading={
                        // The same engine on every card: these are databases
                        // of one server. One nobody has connected is drawn
                        // back a step, as a thing not here yet.
                        <span className={cn("flex", !saved && !current && "opacity-45")}>
                          <EngineMark engine={goes ? engineFor(goes) : engine} size="sm" />
                        </span>
                      }
                      title={<span className="font-mono text-xs">{database.name}</span>}
                      description={
                        <SiblingFacts
                          size={database.size}
                          owner={database.owner}
                          saved={goes?.name}
                        />
                      }
                      trailing={current ? <Tag>this one</Tag> : undefined}
                      actions={
                        !saved && !current && adds ? (
                          <Button
                            size="xs"
                            variant="outline"
                            pending={connecting === database.name}
                            disabled={connecting !== undefined}
                            aria-label={`Connect ${database.name}`}
                            onClick={() => void connect(database.name)}
                          >
                            Connect
                          </Button>
                        ) : undefined
                      }
                    />
                  )
                })}
              </CardGrid>
              <ShowAll count={rows.length} all={all} onToggle={setAll} />
            </>
          )
        }
      </Read>
      {creating && (
        <CreateDatabase
          onClose={close}
          onCreated={(saved) => {
            setCreating(false)
            list.refresh()
            open(saved)
          }}
        />
      )}
    </Block>
  )
}

/** What a sibling database is, on its card's second line: its weight, whose it is, what it is saved as. */
function SiblingFacts({ size, owner, saved }: { size?: number; owner?: string; saved?: string }) {
  const facts: React.ReactNode[] = []
  if (size) facts.push(`${bytes(size)} on disk`)
  if (owner) {
    facts.push(
      <>
        owned by <span style={{ color: nameHue(owner) }}>{owner}</span>
      </>,
    )
  }
  if (saved) facts.push(`saved as ${saved}`)
  if (facts.length === 0) return null
  return facts.map((fact, index) => (
    <span key={index}>
      {index > 0 && " · "}
      {fact}
    </span>
  ))
}

/** The fold under a list cut at `LISTED`: every row, or the first few again. */
function ShowAll({
  count,
  all,
  onToggle,
}: {
  count: number
  all: boolean
  onToggle: (all: boolean) => void
}) {
  if (count <= LISTED) return null
  return (
    <Button
      size="xs"
      variant="ghost"
      className="mt-2 -ml-2"
      aria-expanded={all}
      onClick={() => onToggle(!all)}
    >
      {all ? "Show fewer" : `Show all ${count}`}
    </Button>
  )
}

/** A database name the server's route accepts: letters, digits and underscores. */
const DATABASE_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/

function CreateDatabase({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (saved: DbConnection) => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  const form = useId()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const valid = DATABASE_NAME.test(name)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!valid || busy) return
    setBusy(true)
    setRefused(undefined)
    try {
      const saved = await post<DbConnection>(`/databases/${id}/server/databases`, {
        name,
        connect: true,
      })
      notify.success(`Created ${name}`, { description: `Saved as the connection ${saved.name}.` })
      onCreated(saved)
    } catch (err) {
      setRefused(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title="New database"
      description={`Create an empty database on the server ${conn.name} connects to`}
      size="sm"
      footer={
        <>
          <FormNote className="mr-auto">It is connected with the same account.</FormNote>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={!valid} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <form id={form} onSubmit={submit} className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate text-body font-medium">
              {summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label}
            </p>
            <FormFacts>
              <FormFact label="Server" mono>
                {conn.host}
                {conn.port ? `:${conn.port}` : ""}
              </FormFact>
              {conn.user && (
                <FormFact label="As" mono>
                  {conn.user}
                </FormFact>
              )}
            </FormFacts>
          </div>
        </div>
        <Field
          htmlFor={field}
          label="Name"
          hint="Letters, digits and underscores."
          error={refused ?? (name && !valid ? "Letters, digits and underscores only." : undefined)}
        >
          <Input
            id={field}
            value={name}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
      </form>
    </Modal>
  )
}

/**
 * A key–value server's numbered databases that hold keys. They are not made
 * or connected: every one is a number away from the same connection, so each
 * card opens the keys of that number.
 */
export function Keyspaces({ server }: { server: PollState<RedisServer> }) {
  const { href } = useDatabase()
  const [all, setAll] = useState(false)
  return (
    <Block title="Databases on this server" stale={staleOf(server)}>
      <Read
        poll={server}
        what="the keyspace"
        skeleton={<CardsSkeleton className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3" />}
      >
        {(data) =>
          data.keyspace.length === 0 ? (
            <Quiet>None of its {data.databases} numbered databases holds a key yet.</Quiet>
          ) : (
            <>
              <CardGrid>
                {keyspacesOf(data, all).map((space) => (
                  <ChoiceRow
                    key={space.db}
                    href={href("data", { db: String(space.db) })}
                    verb={`Browse the keys of database ${space.db}`}
                    title={<span className="font-mono text-xs">db {space.db}</span>}
                    description={`${plural(space.keys, "key")} · ${compact(space.expires)} with an expiry`}
                    trailing={space.db === data.db ? <Tag>this one</Tag> : undefined}
                  />
                ))}
              </CardGrid>
              <ShowAll count={data.keyspace.length} all={all} onToggle={setAll} />
              <p className="mt-2 text-hint text-muted-foreground">
                {data.keyspace.length} of its {data.databases} numbered databases hold keys.
              </p>
            </>
          )
        }
      </Read>
    </Block>
  )
}

/** The connection's own database first, then the fullest. */
function keyspacesOf(server: RedisServer, all: boolean) {
  const ordered = [...server.keyspace].sort(
    (a, b) => Number(b.db === server.db) - Number(a.db === server.db) || b.keys - a.keys,
  )
  return all ? ordered : ordered.slice(0, LISTED)
}

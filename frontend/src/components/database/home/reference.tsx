"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Archive, Cpu, File, Globe, LockClosed, Plus } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { bytes, duration, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { Container, DbAccess, DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { WireMark } from "@/components/deploy/wire"
import { Field, FormFact, FormFacts, FormNote, InfoTip } from "@/components/form"
import { Modal } from "@/components/modal"
import { OutcomeStrip, type Outcome } from "@/components/outcome-strip"
import { ProductGlyph } from "@/components/product-logo"
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
  NotUpdating,
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
import { settingsPageHref } from "@/components/database/shell/settings-pages"

/**
 * What a database runs as, where it is reachable from and what is kept of it:
 * one line each, directly under the picture that draws the same three things
 * as nodes and beams. A line is a mark, what the thing is in a word or a name,
 * and one run of facts after it, with the way to the page that changes them
 * at its end. None of them dials the database, so a stopped one keeps them
 * all.
 *
 * They were three columns of label and value rows at the page's foot (Where,
 * Start and stop, From, Listens on, Firewall, Changed, Kept, Newest,
 * Directory): a grey table a third of the page wide, whose values wrapped
 * into sentences. Everything they said is still said, as facts on a line.
 */
export function ServerLines({
  backups,
  answering = true,
  uptimeSeconds,
}: {
  /** The dumps, for an engine that has them. */
  backups?: PollState<DbBackups>
  /** How long the server has been up, where its statistics say. */
  uptimeSeconds?: number
  /** Whether the server is there to dump. The dumps already taken are listed either way. */
  answering?: boolean
}) {
  const { engine } = useDatabase()
  return (
    <div data-slot="server-lines" className="-my-2 flex min-w-0 flex-col">
      <RunsAs uptimeSeconds={answering ? uptimeSeconds : undefined} />
      {engine.can("server") && <ReachableFrom />}
      {backups && <BackupsLine backups={backups} answering={answering} />}
    </div>
  )
}

/**
 * One of those lines. Its facts wrap where the line is narrow and keep a
 * line's height where there are none yet, so a line that is still being read
 * stands as tall as one that has been.
 */
function FactLine({
  name,
  mark,
  reading,
  facts,
  actions,
}: {
  /** What the line is about, which is also its accessible name: "Runs as". */
  name: string
  mark: React.ReactNode
  reading: React.ReactNode
  facts?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <section
      aria-label={name}
      data-slot="fact-line"
      className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-1.5 py-2"
    >
      {mark}
      <div className="min-w-0 flex-[1_1_16rem]">
        <p className="eyebrow">{name}</p>
        <div className="flex min-h-5 min-w-0 items-center gap-2 text-body leading-snug font-medium">
          {reading}
        </div>
        <div className="flex min-h-4 min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-hint leading-snug text-muted-foreground">
          {facts}
        </div>
      </div>
      {actions && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 max-sm:pl-[3.625rem]">
          {actions}
        </div>
      )}
    </section>
  )
}

/** A literal in a run of facts: a name, an address, a path. */
function Mono({ className, ...props }: React.ComponentProps<"span">) {
  return <span className={cn("min-w-0 font-mono wrap-anywhere", className)} {...props} />
}

/**
 * What the server runs as: its container, its unit, its file, or another
 * machine, and how long it has been up by its own count. That last stood on
 * the identity line, and came a moment after the line was drawn.
 */
export function RunsAs({ uptimeSeconds }: { uptimeSeconds?: number }) {
  const { conn, summary, engine, href } = useDatabase()
  const { can } = useAuth()
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
  const power =
    summary.power.via === "docker"
      ? "started and stopped from here, through Docker"
      : summary.power.via === "systemd"
        ? "started and stopped from here, through systemd"
        : (summary.power.reason ?? "not started or stopped from here")

  return (
    <FactLine
      name="Runs as"
      mark={
        <WireMark tone="logo" size="md">
          {container ? (
            <ProductGlyph id="docker" />
          ) : unit ? (
            <ProductGlyph id="systemd" />
          ) : file ? (
            <File aria-hidden />
          ) : summary.source === "remote" ? (
            <Globe aria-hidden />
          ) : (
            <Cpu aria-hidden />
          )}
        </WireMark>
      }
      reading={
        container ? (
          <Mono className="text-xs">{container.name}</Mono>
        ) : unit ? (
          <Mono className="text-xs">{unit.name}</Mono>
        ) : file ? (
          <Mono className="text-xs">{file.path}</Mono>
        ) : (
          <span className="min-w-0 truncate">
            {summary.source === "remote"
              ? `Another machine, at ${conn.host}${conn.port ? `:${conn.port}` : ""}`
              : summary.source === "host"
                ? `A process on this server, listening on port ${conn.port}`
                : "Not known"}
          </span>
        )
      }
      facts={
        <span className="contents">
          {uptimeSeconds !== undefined && (
            <span className="numeric" title="How long the server has been up, by its own count">
              up {duration(uptimeSeconds)}
            </span>
          )}
          {container && (
            <>
              <Mono title="Image">{container.image}</Mono>
              <span title="What Docker says of it">
                {container.status ?? container.state}
                {container.health ? ` · ${container.health}` : ""}
              </span>
              {container.composeProject && (
                <Mono title="Compose project and service">
                  {container.composeProject}
                  {container.composeService ? ` / ${container.composeService}` : ""}
                </Mono>
              )}
              <span data-slot="container-limits">
                <ContainerLimits poll={limits} />
              </span>
              {/* Not for a compose container: its file owns the limits, and
                  the page would only say so. */}
              {can("system.admin") && !container.composeProject && (
                <Link
                  href={settingsPageHref(href("settings"), { path: "resources" })}
                  className="rounded-sm underline focus-ring"
                >
                  Change limits
                </Link>
              )}
              {limits.data?.restartPolicy && (
                <span>
                  restarts <span className="font-mono">{limits.data.restartPolicy}</span>
                </span>
              )}
              {/* Set without asking where MongoDB 8 needs it to start, so it
                  is said where the container is described. */}
              {container.kernelMitigation && <span>{container.kernelMitigation}</span>}
            </>
          )}
          {!container && unit && (
            <span title="What systemd says of it">
              {unit.activeState}
              {unit.subState ? ` (${unit.subState})` : ""}
            </span>
          )}
          {file &&
            (file.exists ? (
              <span>
                {bytes(file.size)} on disk
                {file.modified ? (
                  <span title={timestamp(file.modified)}>
                    {" "}
                    · written {relativeTime(file.modified)}
                  </span>
                ) : null}
              </span>
            ) : (
              <span className="flex">
                <Status tone="danger" label="the file is missing" />
              </span>
            ))}
          <span>{power}</span>
        </span>
      }
      actions={
        <>
          {container && (
            <BlockLink href={`/docker/containers/${encodeURIComponent(container.name)}`}>
              Open container
            </BlockLink>
          )}
          {!container && unit && (
            <BlockLink href={`/processes/services/${encodeURIComponent(unit.name)}`}>
              Open unit
            </BlockLink>
          )}
          {file && (
            <BlockLink href={`/files?path=${encodeURIComponent(directory)}`}>Open folder</BlockLink>
          )}
          {logs && <BlockLink href={href("logs")}>Logs</BlockLink>}
        </>
      }
    />
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
        <span>The limits could not be read: {poll.error.message}</span>{" "}
        <button type="button" className="rounded-sm underline focus-ring" onClick={poll.refresh}>
          Try again
        </button>
      </>
    ) : (
      <Skeleton className="inline-block h-3 w-32 align-middle" />
    )
  }
  if (!data.inspected) return "limits not read while the container is down"
  const memory = data.memoryLimit ? `${bytes(data.memoryLimit, 0)} of memory` : undefined
  const cpu = data.cpuLimit
    ? `${Number(data.cpuLimit.toFixed(2))} ${data.cpuLimit === 1 ? "core" : "cores"}`
    : undefined
  if (!memory && !cpu) return "no limits: it may use all of this machine's memory and processors"
  return (
    <span className="numeric">
      limited to {memory ?? "no memory limit"} · {cpu ?? "no processor quota"}
    </span>
  )
}

const EXPOSURE: Record<string, { word: string; sentence: string }> = {
  local: {
    word: "This server only",
    sentence: "It listens on loopback, so nothing outside this machine can reach it.",
  },
  private: {
    word: "One address of this machine",
    sentence:
      "It listens on one address of this machine — a tailnet, a VPN or a local network — and only that network reaches it.",
  },
  allowlist: {
    word: "Allowed addresses",
    sentence:
      "It listens on every interface, and the gateway refuses every address that is not on its list.",
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
 * for only by one. What the word means in full is behind the ⓘ beside it.
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
  const stale = staleOf(access)
  return (
    <FactLine
      name="Reachable from"
      mark={
        <WireMark tone={open ? "warning" : "logo"} size="md">
          {open ? <Globe aria-hidden /> : <LockClosed aria-hidden />}
        </WireMark>
      }
      reading={
        <>
          {open ? (
            <span className="flex">
              <Status verdict="warning" label={exposure.word} />
            </span>
          ) : (
            <span className="min-w-0 truncate">{exposure.word}</span>
          )}
          <InfoTip label={`What “${exposure.word}” means`}>{exposure.sentence}</InfoTip>
          {stale && <NotUpdating error={stale} />}
        </>
      }
      facts={
        <span className="contents">
          {conn.port && (
            <span>
              listens on{" "}
              <span className="font-mono">
                {conn.host}:{conn.port}
              </span>
            </span>
          )}
          {admin && open && access.data && access.data.publicAddresses.length > 0 && (
            <span>
              public at <Mono>{access.data.publicAddresses.join(", ")}</Mono>
            </span>
          )}
          {admin && access.data && access.data.exposure === "allowlist" && (
            <span>
              allows <Mono>{access.data.allow.join(", ")}</Mono>
            </span>
          )}
          {admin && firewall && (
            <span>
              {firewall.active
                ? `${firewall.backend ?? "the firewall"} is on, and port ${access.data?.port ?? conn.port} is ${firewall.open ? "open" : firewall.allowed.length > 0 ? `open to ${plural(firewall.allowed.length, "listed address", "listed addresses")}` : "closed"}`
                : "no firewall is active on this machine"}
            </span>
          )}
          {admin && access.error && !access.data && (
            <span>
              The firewall could not be read: {access.error.message}{" "}
              <button
                type="button"
                className="rounded-sm underline focus-ring"
                onClick={access.refresh}
              >
                Try again
              </button>
            </span>
          )}
          <span>
            {summary.managed
              ? "changed from Settings: the dashboard owns the binding"
              : summary.source === "remote"
                ? "changed on the machine it runs on"
                : "changed in the server's own configuration"}
          </span>
        </span>
      }
      actions={<BlockLink href={href("settings")}>{admin ? "Change" : "Settings"}</BlockLink>}
    />
  )
}

/**
 * The dumps kept of this database, read every thirty seconds — and every two
 * while one is being taken, since a dump in flight is not in the list and the
 * list is the only place its end shows. The read dials no database, so it is
 * made for a stopped server too; the picture above and the line below share it.
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
 * The dumps kept — the newest few as a strip, how many and how much, the
 * newest one's facts and where they are — and the way to take one now. A dump
 * runs in the background on the server: the line says it is running in the
 * dump's own words, and reports how it ended when the list stops carrying it.
 */
export function BackupsLine({
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
  const data = backups.data
  const job = data?.job
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

  const newest = data?.files[0]
  const kept = data?.files.reduce((sum, file) => sum + file.size, 0) ?? 0
  const none = data !== undefined && data.files.length === 0 && !job
  const stale = staleOf(backups)
  return (
    <FactLine
      name="Backups"
      mark={
        <WireMark tone="logo" size="md">
          <Archive aria-hidden className={cn(newest && "text-success")} />
        </WireMark>
      }
      reading={
        data ? (
          <span className="flex min-w-0 animate-rise items-center gap-x-3">
            {job ? (
              <span className="flex text-body font-medium" data-slot="backup-running">
                <TextShimmer>{`${job.title}…`}</TextShimmer>
              </span>
            ) : none ? (
              <span className="min-w-0 truncate font-normal text-muted-foreground">
                No dump of {name} has been taken here
              </span>
            ) : null}
            <OutcomeStrip
              items={outcomes}
              label={`The last ${plural(Math.min(data.files.length, STRIP), "dump")}${job ? ", and one running now" : ""}`}
            />
            {data.files.length > 0 && (
              <span className="numeric min-w-0 truncate">
                {plural(data.files.length, "dump")} · {bytes(kept)}
              </span>
            )}
            {stale && <NotUpdating error={stale} />}
          </span>
        ) : backups.error ? (
          <span className="font-normal text-muted-foreground">The dumps could not be read</span>
        ) : (
          <Skeleton className="h-3 w-40" />
        )
      }
      facts={
        data ? (
          <span className="contents">
            {newest && (
              <span>
                newest <span title={timestamp(newest.takenAt)}>{relativeTime(newest.takenAt)}</span>
                {" · "}
                {bytes(newest.size)}
                {" · "}
                {newest.tool ?? newest.format}
                {newest.summary ? ` · ${newest.summary}` : ""}
              </span>
            )}
            {none && (
              <span>
                a dump is one file Backups can restore from
                {answering ? "" : "; one can be taken once the server answers"}
              </span>
            )}
            <Mono title="The dashboard's dump directory for this database">{data.dir}</Mono>
          </span>
        ) : backups.error ? (
          <span>
            {errorMessage(backups.error)}{" "}
            <button type="button" className="rounded-sm underline focus-ring" onClick={refresh}>
              Try again
            </button>
          </span>
        ) : (
          <Skeleton className="h-2.5 w-56" />
        )
      }
      actions={
        <>
          {data && !job && answering && can("service.control") && (
            <Button size="xs" variant="outline" pending={starting} onClick={start}>
              <Archive />
              Back up now
            </Button>
          )}
          <BlockLink href={href("backups")}>All backups</BlockLink>
        </>
      }
    />
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
    <Block
      title="Databases on this server"
      stale={staleOf(server)}
      actions={
        server.data &&
        server.data.keyspace.length > 0 && (
          <span className="numeric text-hint text-muted-foreground">
            {server.data.keyspace.length} of its {server.data.databases} numbered databases hold
            keys
          </span>
        )
      }
    >
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

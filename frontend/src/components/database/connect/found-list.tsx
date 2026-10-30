"use client"

import { useState, type ReactNode } from "react"
import { Eye, EyeOff } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { DimActions, IconAction } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { engineOf } from "@/components/database/engine"
import {
  asSentence,
  foundActionWords,
  foundShelves,
  instanceName,
  instanceState,
  instanceWhere,
  scanNotes,
  scanSilences,
  scanningFiles,
  whyWaiting,
  type FoundShelves,
  type WaitingServer,
} from "@/components/database/connect/inventory"
import type { Found } from "@/components/database/connect/use-found"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import type { DbInstance } from "@/components/database/fleet/types"
import type { InventoryData } from "@/components/database/fleet/use-fleet"
import { EngineMark } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

/** How many of a shelf are drawn before the rest fold behind a count. */
const SERVER_ROWS = 8
const FILE_ROWS = 6

/**
 * Everything discovery found on this machine that no saved connection points
 * at: servers in containers and installed on the host, running or not;
 * database files; engines the dashboard can see and not open.
 *
 * Each is something you take, so each is a lit row whose press is the one
 * thing to do with it — connect it, open the file, start what is down, or go
 * to the container or unit that owns it. What stops a row being connected is
 * the second line of its row, after where it is. What is not something to
 * take — a program's own state, a copy kept aside, a data directory with no
 * server, what the operator ignored — is counted behind one fold rather than
 * listed among things to act on, and a long shelf shows its first rows and
 * counts the rest.
 *
 * A collector that could not read is said so above the list: an empty list
 * under a Docker that did not answer is not a server with no containers. And
 * when the whole reading failed, the servers the fleet itself reported as
 * waiting for a password are still listed under the failure, so the one thing
 * the reader came to do is not lost with it.
 */
export function FoundList({
  inventory,
  found,
  fresh,
  first,
  waiting,
  refusals,
}: {
  inventory: InventoryData
  found: Found
  /** The rows arrive staggered: for a list drawn once, not one re-read under a poll. */
  fresh?: boolean
  /** The instance the page was opened for, which stands first. */
  first?: string
  /** What the fleet says is waiting for a password, for when this reading has failed. */
  waiting?: WaitingServer[]
  /** What a container answered the last time it was tried, by its name. */
  refusals?: ReadonlyMap<string, string>
}) {
  const [allServers, setAllServers] = useState(false)
  const [allFiles, setAllFiles] = useState(false)
  const data = inventory.data

  if (!data) {
    if (inventory.error) {
      return (
        <div className="min-w-0 space-y-5">
          <ReadFailed error={inventory.error} onRetry={inventory.refresh} every="30 seconds" />
          {waiting && waiting.length > 0 && <Reported waiting={waiting} found={found} />}
        </div>
      )
    }
    return (
      <div className="space-y-2" role="status" aria-label="Looking at this server">
        {[0, 1, 2].map((row) => (
          <Skeleton key={row} className="h-14 w-full rounded-xl" />
        ))}
      </div>
    )
  }

  const shelves = foundShelves(data)
  const silences = scanSilences(data.scans)
  const notes = scanNotes(data.scans)
  const servers = [...shelves.servers].sort(
    (a, b) => Number(b.key === first) - Number(a.key === first),
  )
  const files = [...shelves.files].sort((a, b) => Number(b.key === first) - Number(a.key === first))
  const shownServers = allServers ? servers : servers.slice(0, SERVER_ROWS)
  const shownFiles = allFiles ? files : files.slice(0, FILE_ROWS)
  const nothing = servers.length === 0 && files.length === 0
  const row = (instance: DbInstance, index: number) => (
    <FoundRow
      key={instance.key}
      instance={instance}
      found={found}
      refusal={refusals?.get(instance.name)}
      index={fresh ? index : undefined}
    />
  )

  return (
    <div className="min-w-0 space-y-5">
      {(silences.length > 0 || notes.length > 0 || inventory.scanning) && (
        <div className="space-y-1 text-hint leading-relaxed text-muted-foreground">
          {inventory.scanning && (
            <TextShimmer>Scanning this server for database files…</TextShimmer>
          )}
          {silences.map((silence) => (
            <p key={silence.what}>
              <span className="font-medium text-foreground/85">Not looked at: {silence.what}.</span>{" "}
              {silence.reason}
            </p>
          ))}
          {notes.map((note) => (
            <p key={note}>{asSentence(note)}</p>
          ))}
        </div>
      )}

      {nothing && (
        <EmptyNote className="px-0 text-left">
          {scanningFiles(data.scans)
            ? "Still looking for database files. Nothing else on this server is waiting to be connected."
            : "Everything found on this server is connected."}
        </EmptyNote>
      )}

      {servers.length > 0 && (
        <div className="space-y-2.5">
          <GroupRule label="Servers" count={servers.length} />
          <ChoiceList>{shownServers.map(row)}</ChoiceList>
          {servers.length > SERVER_ROWS && (
            <More
              all={allServers}
              rest={servers.length - SERVER_ROWS}
              onToggle={() => setAllServers((all) => !all)}
            />
          )}
        </div>
      )}

      {files.length > 0 && (
        <div className="space-y-2.5">
          <GroupRule label="Files" count={files.length} />
          <ChoiceList>{shownFiles.map(row)}</ChoiceList>
          {files.length > FILE_ROWS && (
            <More
              all={allFiles}
              rest={files.length - FILE_ROWS}
              onToggle={() => setAllFiles((all) => !all)}
            />
          )}
        </div>
      )}

      <SetAside shelves={shelves} found={found} row={row} />
    </div>
  )
}

/** The groups of what is set aside, in the order the fold lists them. */
const ASIDE = [
  {
    key: "kept",
    label: "Other programs",
    fact: (n: number) => `${n} kept by other programs`,
  },
  {
    key: "backups",
    label: "Backups and copies",
    fact: (n: number) => `${n} ${n === 1 ? "copy" : "copies"}`,
  },
  { key: "leftover", label: "No server", fact: (n: number) => `${n} without a server` },
  { key: "ignored", label: "Ignored", fact: (n: number) => `${n} ignored` },
] as const

type AsideKey = (typeof ASIDE)[number]["key"]

/**
 * Everything found that is not something to take, behind one fold: a
 * program's own state, copies kept aside, data directories no server runs on,
 * and what the operator ignored. Closed, it is one line that says what it
 * holds; open, one group at a time, picked by its chip, so twenty files of a
 * browser's profile never push the ignored servers off the screen.
 *
 * An ignore mark is kept after the thing it names is gone — a container
 * recreated under its name is still left alone — so those are not drawn as
 * rows of bare keys but counted on one line, with the way to forget them.
 */
function SetAside({
  shelves,
  found,
  row,
}: {
  shelves: FoundShelves
  found: Found
  row: (instance: DbInstance, index: number) => ReactNode
}) {
  const counts: Record<AsideKey, number> = {
    kept: shelves.kept.length,
    backups: shelves.backups.length,
    leftover: shelves.leftover.length,
    ignored: shelves.ignored.length + shelves.gone.length,
  }
  const groups = ASIDE.filter((group) => counts[group.key] > 0)
  const [picked, setPicked] = useState<AsideKey | null>(null)
  const [all, setAll] = useState(false)
  if (groups.length === 0) return null
  const current = groups.find((group) => group.key === picked) ?? groups[0]
  const total = groups.reduce((sum, group) => sum + counts[group.key], 0)
  const rows = current.key === "ignored" ? shelves.ignored : shelves[current.key]
  const shown = all ? rows : rows.slice(0, ASIDE_ROWS)

  return (
    <Disclosure
      quiet
      summary={
        <span className="flex items-baseline gap-2">
          <span>Set aside</span>
          <span className="numeric text-hint text-muted-foreground">{total}</span>
        </span>
      }
      facts={groups.map((group) => group.fact(counts[group.key])).join(" · ")}
    >
      <div className="mt-2 space-y-3">
        {groups.length > 1 && (
          <ChipStrip role="group" aria-label="What is set aside">
            {groups.map((group) => (
              <FilterChip
                key={group.key}
                selected={group.key === current.key}
                onClick={() => {
                  setPicked(group.key)
                  setAll(false)
                }}
              >
                {group.label} <ChipCount>{counts[group.key]}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        )}
        <p className="text-hint text-muted-foreground">{ASIDE_WHY[current.key]}</p>
        {shown.length > 0 && <ChoiceList>{shown.map((instance) => row(instance, 0))}</ChoiceList>}
        {rows.length > ASIDE_ROWS && (
          <More
            all={all}
            rest={rows.length - ASIDE_ROWS}
            onToggle={() => setAll((held) => !held)}
          />
        )}
        {current.key === "ignored" && shelves.gone.length > 0 && (
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-xl border border-dashed px-3 py-2.5">
            <span className="min-w-0 text-hint text-muted-foreground">
              {shelves.gone.length === 1
                ? "1 ignored item is no longer on this server."
                : `${shelves.gone.length} ignored items are no longer on this server.`}
            </span>
            <Button
              size="xs"
              variant="outline"
              pending={found.forgetting}
              onClick={() => void found.forget(shelves.gone)}
            >
              Forget {shelves.gone.length === 1 ? "it" : "them"}
            </Button>
          </div>
        )}
      </div>
    </Disclosure>
  )
}

/** How many rows of one set-aside group are drawn before the rest fold behind a count. */
const ASIDE_ROWS = 6

/** What each set-aside group is, in a sentence above its rows. */
const ASIDE_WHY: Record<AsideKey, string> = {
  kept: "Files programs keep for themselves — browser profiles, caches, the dashboard's own store. Nothing to connect.",
  backups:
    "Copies kept aside: backups, and the dashboard's store under its own name somewhere else. They can still be opened.",
  leftover:
    "Data directories no server runs on — usually a volume a removed container left behind.",
  ignored: "What you said to leave alone. Connecting all skips these.",
}

/** The rest of a long shelf, behind its count. */
function More({ all, rest, onToggle }: { all: boolean; rest: number; onToggle: () => void }) {
  return (
    <Button
      size="xs"
      variant="ghost"
      className="text-muted-foreground"
      aria-expanded={all}
      onClick={onToggle}
    >
      {all ? "Show fewer" : `Show ${rest} more`}
    </Button>
  )
}

/**
 * The servers the fleet reported as waiting for a password, listed where the
 * inventory would have been. A server installed on this machine is connected
 * by its address from here; a container needs the inventory's own reading,
 * so its row says why it waits and leaves the press to the retry above.
 */
function Reported({ waiting, found }: { waiting: WaitingServer[]; found: Found }) {
  const { drivers } = useDatabases()
  return (
    <div className="space-y-2.5">
      <GroupRule label="Reported with the fleet" count={waiting.length} />
      <ChoiceList>
        {waiting.map((server) => {
          const engine = engineOf(server.engine, drivers)
          const host = server.via.kind === "host" ? server.via.server : undefined
          return (
            <ChoiceRow
              key={server.id}
              disabled={!host}
              onSelect={() => host && found.askHost(host)}
              verb={`Connect ${server.name}`}
              leading={<EngineMark engine={engine} size="sm" />}
              title={
                <>
                  {server.name}
                  <span className="ml-2 text-hint font-normal text-muted-foreground">
                    {engine.label}
                  </span>
                </>
              }
              description={
                host ? <span className="font-mono">{`${host.host}:${host.port}`}</span> : undefined
              }
              trailing={
                host && <span className="text-xs font-medium whitespace-nowrap">Connect</span>
              }
            >
              {server.reason && (
                <p className="pl-11 text-hint leading-relaxed text-muted-foreground">
                  {server.reason}
                </p>
              )}
            </ChoiceRow>
          )
        })}
      </ChoiceList>
    </div>
  )
}

function FoundRow({
  instance,
  found,
  refusal,
  index,
}: {
  instance: DbInstance
  found: Found
  /** What it answered the last time it was tried with what it states. */
  refusal?: string
  index?: number
}) {
  const engine = found.engineOfInstance(instance)
  const action = found.actionOf(instance)
  const name = instanceName(instance)
  const words = foundActionWords(action, name)
  const where = instanceWhere(instance)
  const busy = found.busyWord(instance)
  const failure = found.failures[instance.key]
  // What stands between it and a connection, as one sentence: what it
  // answered the last time everything was tried, the password nothing here
  // states, or why it cannot be connected at all. One that only needs its
  // press has nothing to explain. It shares the second line with where the
  // thing is, so every row is the same two lines; a failure just now is the
  // exception, and is said in full under the row until it is tried again.
  const wantsPassword = action.kind === "connect" && Boolean(refusal?.includes("connect it with"))
  // A MongoDB this kernel keeps from starting is said as that, not as the
  // dial it refuses.
  const why =
    action.kind === "kernel-mitigation"
      ? action.problem.message
      : (asSentence(refusal) ??
        (instance.connectable ? whyWaiting(instance) : asSentence(instance.reason)))
  const file = instance.file && instance.kind !== "embedded" ? instance.file : undefined

  return (
    <ChoiceRow
      index={index}
      busy={Boolean(busy)}
      disabled={action.kind === "none" || Boolean(busy)}
      href={action.kind === "look" ? action.href : undefined}
      // One that already refused what it states is not tried with it again:
      // the server's own sentence asks for the password, so the press opens
      // the form.
      onSelect={() => (wantsPassword ? found.ask(instance) : found.act(instance))}
      verb={words.verb}
      leading={<EngineMark engine={engine} size="sm" />}
      title={
        <span title={instance.name}>
          {name}
          <span className="ml-2 text-hint font-normal text-muted-foreground">
            {engine.label}
            {instance.version ? ` ${instance.version}` : ""}
          </span>
        </span>
      }
      description={
        <span className="flex min-w-0 items-baseline gap-1.5">
          {instance.kind === "file" ? (
            <FolderOf path={where.text} className={why ? "max-w-[45%] shrink-0" : undefined} />
          ) : (
            <span
              title={where.text}
              className={cn("min-w-0 truncate font-mono", why && "max-w-[45%] shrink-0")}
            >
              {where.text}
            </span>
          )}
          {why && (
            <>
              <span aria-hidden>·</span>
              <span title={why} className="min-w-0 flex-1 truncate">
                {why}
              </span>
            </>
          )}
        </span>
      }
      trailing={
        <span className="flex shrink-0 items-center gap-3">
          {instance.driver === "" && instance.kind === "server" && (
            <Tag className="max-sm:hidden">no driver yet</Tag>
          )}
          {busy ? (
            <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
          ) : file ? (
            <span className="numeric text-hint whitespace-nowrap text-muted-foreground max-sm:hidden">
              {/* A data directory is not measured: its size would read 0 B. */}
              {instance.kind === "data"
                ? `changed ${relativeTime(file.modified)}`
                : `${bytes(file.size)}${file.wal ? " · in use" : ` · ${relativeTime(file.modified)}`}`}
            </span>
          ) : (
            <Status
              state={instance.state}
              label={instanceState(instance)}
              className="max-sm:hidden"
            />
          )}
          {words.word && !busy && (
            // One width for every verb, so the readings beside them line up
            // down the list instead of stepping with the word's length.
            <span className="w-24 text-right text-xs font-medium whitespace-nowrap max-sm:w-auto">
              {words.word}
            </span>
          )}
        </span>
      }
      actions={
        !instance.self && (
          <DimActions>
            <IconAction
              label={instance.ignored ? `Stop ignoring ${name}` : `Ignore ${name}`}
              disabled={Boolean(busy)}
              onClick={() => void found.ignore(instance.key, !instance.ignored)}
            >
              {instance.ignored ? <Eye /> : <EyeOff />}
            </IconAction>
          </DimActions>
        )
      }
    >
      {failure && (
        <p className="pl-11 text-hint leading-relaxed break-words text-destructive">{failure}</p>
      )}
    </ChoiceRow>
  )
}

/**
 * The folder a file is in, under its name. The name is the row's title, so
 * the whole path said it twice; and where the folder is too long for the
 * row, it is cut from its start, because the folder nearest the file is what
 * tells two files of one name apart.
 */
function FolderOf({ path, className }: { path: string; className?: string }) {
  const cut = path.lastIndexOf("/")
  return (
    <span
      dir="rtl"
      title={path}
      className={cn("block min-w-0 truncate text-left font-mono", className)}
    >
      <bdi dir="ltr">{cut > 0 ? path.slice(0, cut) : "/"}</bdi>
    </span>
  )
}

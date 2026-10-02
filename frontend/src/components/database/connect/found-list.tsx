"use client"

import { useState } from "react"
import { Eye, EyeOff } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Disclosure } from "@/components/form"
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
  instanceState,
  instanceWhere,
  scanNotes,
  scanSilences,
  scanningFiles,
  whyWaiting,
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
const FILE_ROWS = 4

/**
 * Everything discovery found on this machine that no saved connection points
 * at: servers in containers and installed on the host, running or not;
 * database files; engines the dashboard can see and not open.
 *
 * Each is something you take, so each is a lit row whose press is the one
 * thing to do with it — connect it, open the file, start what is down, or go
 * to the container or unit that owns it. What stops a row being connected is
 * the line under it, as a sentence. Files that are some program's private
 * state and what the operator said to leave alone are counted behind folds
 * rather than listed among things to act on, and a long shelf shows its first
 * rows and counts the rest.
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

      {shelves.kept.length > 0 && (
        <Disclosure
          quiet
          summary={
            <span className="flex items-baseline gap-2">
              <span>Kept by other programs</span>
              <span className="numeric text-hint text-muted-foreground">{shelves.kept.length}</span>
            </span>
          }
        >
          <ChoiceList>{shelves.kept.map((instance) => row(instance, 0))}</ChoiceList>
        </Disclosure>
      )}

      {shelves.ignored.length + shelves.gone.length > 0 && (
        <Disclosure
          quiet
          summary={
            <span className="flex items-baseline gap-2">
              <span>Ignored</span>
              <span className="numeric text-hint text-muted-foreground">
                {shelves.ignored.length + shelves.gone.length}
              </span>
            </span>
          }
        >
          <ChoiceList>
            {shelves.ignored.map((instance) => row(instance, 0))}
            {shelves.gone.map((key) => (
              <ChoiceRow
                key={key}
                disabled
                verb={`Ignored ${key}`}
                title={<span className="font-mono">{key}</span>}
                description="No longer on this server."
                actions={
                  <DimActions>
                    <IconAction
                      label={`Stop ignoring ${key}`}
                      onClick={() => void found.ignore(key, false)}
                    >
                      <Eye />
                    </IconAction>
                  </DimActions>
                }
              />
            ))}
          </ChoiceList>
        </Disclosure>
      )}
    </div>
  )
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
  const words = foundActionWords(action, instance.name)
  const where = instanceWhere(instance)
  const busy = found.busyWord(instance)
  const failure = found.failures[instance.key]
  // What stands between it and a connection, as one sentence: what it just
  // answered, what it answered the last time everything was tried, the
  // password nothing here states, or why it cannot be connected at all. One
  // that only needs its press has nothing to explain.
  const wantsPassword = action.kind === "connect" && Boolean(refusal?.includes("connect it with"))
  const why =
    failure ??
    asSentence(refusal) ??
    (instance.connectable ? whyWaiting(instance) : asSentence(instance.reason))

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
        <>
          {instance.name}
          <span className="ml-2 text-hint font-normal text-muted-foreground">
            {engine.label}
            {instance.version ? ` ${instance.version}` : ""}
          </span>
        </>
      }
      description={<span className="font-mono">{where.text}</span>}
      trailing={
        <span className="flex shrink-0 items-center gap-3">
          {instance.driver === "" && instance.kind === "server" && (
            <Tag className="max-sm:hidden">no driver yet</Tag>
          )}
          {busy ? (
            <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
          ) : instance.file && instance.kind !== "embedded" ? (
            <span className="numeric text-hint whitespace-nowrap text-muted-foreground max-sm:hidden">
              {bytes(instance.file.size)}
              {instance.file.wal ? " · in use" : ` · ${relativeTime(instance.file.modified)}`}
            </span>
          ) : (
            <Status
              state={instance.state}
              label={instanceState(instance)}
              className="max-sm:hidden"
            />
          )}
          {words.word && !busy && (
            <span className="text-xs font-medium whitespace-nowrap">{words.word}</span>
          )}
        </span>
      }
      actions={
        !instance.self && (
          <DimActions>
            <IconAction
              label={
                instance.ignored ? `Stop ignoring ${instance.name}` : `Ignore ${instance.name}`
              }
              disabled={Boolean(busy)}
              onClick={() => void found.ignore(instance.key, !instance.ignored)}
            >
              {instance.ignored ? <Eye /> : <EyeOff />}
            </IconAction>
          </DimActions>
        )
      }
    >
      {why && (
        <p
          className={
            failure
              ? "pl-11 text-hint leading-relaxed break-words text-destructive"
              : "pl-11 text-hint leading-relaxed text-muted-foreground"
          }
        >
          {why}
        </p>
      )}
    </ChoiceRow>
  )
}

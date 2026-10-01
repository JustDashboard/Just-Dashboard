"use client"

import { useState } from "react"
import { Eye, EyeOff } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { EmptyNote, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  foundShelves,
  instanceState,
  instanceWhere,
  scanNotes,
  scanSilences,
  scanningFiles,
  type FoundAction,
} from "@/components/database/connect/inventory"
import type { Found } from "@/components/database/connect/use-found"
import type { DbInstance } from "@/components/database/fleet/types"
import type { InventoryData } from "@/components/database/fleet/use-fleet"
import { EngineMark } from "@/components/database/kit"

/** How many of a shelf are drawn before the rest fold behind a count. */
const SHELF_ROWS = 6

/**
 * Everything discovery found on this machine that no saved connection points
 * at: servers in containers and installed on the host, running or not;
 * database files; engines the dashboard can see and not open.
 *
 * Each is something you take, so each is a lit row whose press is the one
 * thing to do with it — connect it, open the file, start what is down, or go
 * to the container or unit that owns it. What stops a row being connected is
 * its second line, in the server's own sentence. Files that are some
 * program's private state and what the operator said to leave alone are
 * counted behind folds rather than listed among things to act on.
 *
 * A collector that could not read is said so above the list: an empty list
 * under a Docker that did not answer is not a server with no containers.
 */
export function FoundList({
  inventory,
  found,
  fresh,
  first,
}: {
  inventory: InventoryData
  found: Found
  /** The rows arrive staggered: for a list drawn once, not one re-read under a poll. */
  fresh?: boolean
  /** The instance the page was opened for, which stands first. */
  first?: string
}) {
  const [allFiles, setAllFiles] = useState(false)
  const data = inventory.data

  if (!data) {
    if (inventory.error) return <ErrorState error={inventory.error} onRetry={inventory.refresh} />
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
  const shownFiles = allFiles ? files : files.slice(0, SHELF_ROWS)
  const nothing = servers.length === 0 && files.length === 0
  const row = (instance: DbInstance, index: number) => (
    <FoundRow
      key={instance.key}
      instance={instance}
      found={found}
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
            <p key={note}>{note}</p>
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
          <ChoiceList>{servers.map(row)}</ChoiceList>
        </div>
      )}

      {files.length > 0 && (
        <div className="space-y-2.5">
          <GroupRule label="Files" count={files.length} />
          <ChoiceList>{shownFiles.map(row)}</ChoiceList>
          {files.length > SHELF_ROWS && (
            <Button
              size="xs"
              variant="ghost"
              className="text-muted-foreground"
              onClick={() => setAllFiles((all) => !all)}
            >
              {allFiles ? "Show fewer" : `Show ${files.length - SHELF_ROWS} more`}
            </Button>
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

/** The word a row's press is named with, and the one drawn before its arrow. */
function actionWords(action: FoundAction, name: string): { verb: string; word: string } {
  switch (action.kind) {
    case "connect":
    case "credentials":
      return { verb: `Connect ${name}`, word: "Connect" }
    case "open":
      return { verb: `Open ${name}`, word: "Open file" }
    case "start-container":
      return { verb: `Start ${name}`, word: "Start container" }
    case "start-unit":
      return { verb: `Start ${name}`, word: "Start service" }
    case "look":
      return { verb: `${action.label} ${name}`, word: action.label }
    case "none":
      return { verb: name, word: "" }
  }
}

function FoundRow({
  instance,
  found,
  index,
}: {
  instance: DbInstance
  found: Found
  index?: number
}) {
  const engine = found.engineOfInstance(instance)
  const action = found.actionOf(instance)
  const words = actionWords(action, instance.name)
  const where = instanceWhere(instance)
  const busy = found.busyWord(instance)
  const failure = found.failures[instance.key]
  // What stops it being connected, in the server's sentence; for one that
  // can be, nothing — its press says what happens.
  const why = failure ?? (instance.connectable ? undefined : instance.reason)

  return (
    <ChoiceRow
      index={index}
      busy={Boolean(busy)}
      disabled={action.kind === "none" || Boolean(busy)}
      href={action.kind === "look" ? action.href : undefined}
      onSelect={() => found.act(instance)}
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
          {instance.driver === "" && <Tag className="max-sm:hidden">no driver yet</Tag>}
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
              ? "text-hint leading-relaxed break-words text-destructive"
              : "text-hint leading-relaxed text-muted-foreground"
          }
        >
          {why}
        </p>
      )}
    </ChoiceRow>
  )
}

"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { Pencil, Play, Trash } from "@/components/icons"
import {
  osLabel,
  protocolLabel,
  reachability,
  sessionHref,
  type RdComputerView,
} from "@/lib/remote-desktop"
import { cn } from "@/lib/utils"
import { ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { VerbMenu } from "@/components/verbs"
import { ComputerMark, reachTone } from "@/components/remote-desktop/marks"
import {
  readingLine,
  signInAddress,
  signInGap,
  stateWord,
} from "@/components/remote-desktop/computer-reading"

/**
 * One computer on the Remote Desktop page: a one-line row you open, drawn as
 * its system.
 *
 * Left to right it answers what the reader asks before connecting: which
 * machine (its system's mark, its name over the server on it and where the
 * dashboard signs in), whether it can be reached now (a dot and a word), how
 * far it is and when it was last used (one line), then Connect, Stream beside
 * it once the computer is paired with Sunshine, and the menu. It was a 200px
 * card with three figure cells under a picture that already draws every
 * computer; a row is one height whatever the computer's state, and its
 * columns line up down the list.
 *
 * A press anywhere on the row opens the session, except on a control of its
 * own. A computer in a session runs a light round its edge. On a phone the
 * row is the mark, the name over its state and reading, and the menu: the
 * press that opens it is the row itself.
 */
export function ComputerRow({
  computer,
  index,
  onEdit,
  onRemove,
}: {
  computer: RdComputerView
  index: number
  onEdit: () => void
  onRemove: () => void
}) {
  const router = useRouter()
  const href = sessionHref(computer.id)
  const reach = reachability(computer)
  const state = stateWord(reach)
  const reading = readingLine(computer, reach)
  const gap = signInGap(computer)
  const off = reach.kind === "offline"
  const status = (
    <Status
      tone={reachTone(reach)}
      live={reach.kind === "live"}
      label={state}
      className="min-w-0"
    />
  )
  return (
    <ChoiceRow
      index={index}
      href={href}
      verb={`Open the screen of ${computer.name}`}
      busy={reach.kind === "live"}
      leading={
        <span role="img" aria-label={osLabel(computer.os)} title={osLabel(computer.os)}>
          <ComputerMark os={computer.os} size="sm" className={cn(off && "opacity-60")} />
        </span>
      }
      title={computer.name}
      description={
        <>
          <span className="block truncate max-sm:hidden">
            {protocolLabel(computer.protocol, computer.os)} ·{" "}
            <span className="font-mono">{signInAddress(computer)}</span>
            {gap && <span className="text-warning"> · {gap}</span>}
          </span>
          {/* A phone has no room for the columns: the state and the reading
              are the second line, and what is not saved takes the reading's
              place because it is what the session will stop to ask for. */}
          <span className="flex min-w-0 items-center gap-1.5 sm:hidden" title={reach.label}>
            {status}
            <span className={cn("min-w-0 truncate", gap && "text-warning")}>
              · {gap ?? reading}
            </span>
          </span>
        </>
      }
      trailing={
        <>
          <span
            data-slot="computer-state"
            data-state={reach.kind}
            className="flex w-52 min-w-0 shrink-0 flex-col gap-0.5 max-sm:hidden xl:w-auto xl:flex-row xl:items-center xl:gap-4"
          >
            <span className="flex min-w-0 xl:w-28" title={reach.label}>
              {status}
            </span>
            <span className="numeric min-w-0 truncate text-hint text-muted-foreground xl:w-52">
              {reading}
            </span>
          </span>
          {/* One width whether or not Stream is offered, so Connect stands on
              one edge down the list. */}
          <span className="flex w-36 shrink-0 items-center justify-end gap-1.5 max-sm:hidden">
            {computer.streamHostId > 0 && (
              <Button size="xs" variant="ghost" asChild>
                <Link
                  href={`${href}?mode=stream`}
                  aria-label={`Stream ${computer.name} with Sunshine`}
                >
                  Stream
                </Link>
              </Button>
            )}
            <Button size="xs" variant="outline" asChild>
              <Link href={href} aria-label={`Connect to ${computer.name}`}>
                <Play className="size-3" />
                Connect
              </Link>
            </Button>
          </span>
        </>
      }
      actions={
        <VerbMenu
          label={`Actions for ${computer.name}`}
          verbs={[
            {
              key: "stream",
              label: "Stream with Sunshine",
              icon: Play,
              run: () => router.push(`${href}?mode=stream`),
            },
            { key: "edit", label: "Edit", icon: Pencil, run: onEdit },
            { key: "remove", label: "Remove", icon: Trash, run: onRemove, danger: true },
          ]}
        />
      }
    />
  )
}

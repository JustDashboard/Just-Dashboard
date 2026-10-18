"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Pencil, Play, Trash } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import {
  isoOf,
  latency,
  osLabel,
  protocolLabel,
  reachability,
  sessionHref,
  type RdComputerView,
} from "@/lib/remote-desktop"
import { cn } from "@/lib/utils"
import { CONTROL } from "@/components/flow"
import { Metric } from "@/components/page"
import { Status } from "@/components/status-dot"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu } from "@/components/verbs"
import { ComputerMark, reachTone } from "@/components/remote-desktop/marks"

/**
 * One computer on the Remote Desktop page: a card you open, drawn as its
 * system.
 *
 * Top to bottom it answers what the reader asks before connecting: which
 * machine and whether it can be reached now; how far it is, by which path,
 * and when it was last used; and the quiet facts — its address and how the
 * dashboard signs in to it — with Connect at the foot, and Stream beside it
 * once the computer is paired with Sunshine. A press anywhere on
 * the card opens the session, except on a control of its own. A computer in
 * a session runs a light round its edge.
 */
export function ComputerCard({
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
  const off = reach.kind === "offline"
  const server = protocolLabel(computer.protocol, computer.os)
  const path = computer.peer
    ? computer.peer.direct
      ? "Direct"
      : computer.peer.relay
        ? `Relay ${computer.peer.relay}`
        : "Tailnet"
    : "Network"
  return (
    <li className="min-w-0" data-slot="choice-row" data-card="computer" data-state={reach.kind}>
      <BlurFade delay={Math.min(index, 11) * 0.04} className="h-full">
        <SpotlightBorder className="h-full">
          {reach.kind === "live" && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={80} duration={4} />
            </span>
          )}
          <div
            onClick={(event) => {
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              router.push(href)
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col gap-3.5 rounded-xl p-4"
          >
            <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
              <ComputerMark os={computer.os} className={cn("row-span-2", off && "opacity-60")} />
              <Link
                href={href}
                className="block max-w-full min-w-0 truncate rounded-sm text-title leading-tight font-semibold tracking-tight focus-ring"
              >
                {computer.name}
              </Link>
              <span className="-my-1 -mr-1.5 flex items-center gap-1">
                <ArrowRight
                  aria-hidden
                  className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
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
              </span>
              <div className="col-span-2 col-start-2 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                <Status tone={reachTone(reach)} live={reach.kind === "live"} label={reach.label} />
                <span className="min-w-0 flex-1 truncate pr-0.5">
                  {osLabel(computer.os)} · {server}
                </span>
              </div>
            </div>

            <div
              className={cn(
                "grid min-h-10 grid-cols-3 gap-x-2.5 [&>*+*]:border-l [&>*+*]:border-hairline [&>*+*]:pl-2.5",
                off && "opacity-60",
              )}
            >
              <Metric label="Round trip" value={latency(computer.reach.latencyMs) ?? "—"} />
              <Metric label="Path" value={reach.kind === "offline" ? "—" : path} />
              <Metric
                label="Last used"
                value={
                  computer.lastSessionAt ? relativeTime(isoOf(computer.lastSessionAt)) : "never"
                }
              />
            </div>

            <div className="mt-auto flex min-w-0 flex-col gap-2 border-t border-hairline pt-3 text-hint text-muted-foreground">
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                {computer.username ? (
                  <span className="min-w-0 truncate">signs in as {computer.username}</span>
                ) : (
                  computer.protocol === "rdp" && (
                    <span className="text-warning">no account saved</span>
                  )
                )}
                {!computer.hasPassword && <span className="text-warning">no password saved</span>}
                {computer.live > 0 && (
                  <span className="font-medium text-success">in a session now</span>
                )}
              </div>
              <div className="flex min-w-0 items-center justify-between gap-3">
                <span className="min-w-0 truncate pr-0.5 font-mono" title={computer.host}>
                  {computer.host}:{computer.port}
                </span>
                <span className="-my-1 flex shrink-0 items-center gap-1.5">
                  {computer.streamHostId > 0 && (
                    <Button size="xs" variant="ghost" className="text-foreground" asChild>
                      <Link
                        href={`${href}?mode=stream`}
                        aria-label={`Stream ${computer.name} with Sunshine`}
                      >
                        Stream
                      </Link>
                    </Button>
                  )}
                  <Button size="xs" variant="outline" className="text-foreground" asChild>
                    <Link href={href} aria-label={`Connect to ${computer.name}`}>
                      <Play className="size-3" />
                      Connect
                    </Link>
                  </Button>
                </span>
              </div>
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

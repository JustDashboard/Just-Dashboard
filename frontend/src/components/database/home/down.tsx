"use client"

import Link from "next/link"
import { Pause, Play, Stop, Warning } from "@/components/icons"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { HomeIdentity } from "@/components/database/home/identity"
import { Columns } from "@/components/database/home/layout"
import { POWER } from "@/components/database/home/power"
import {
  BackupsBlock,
  ReachableFrom,
  RunsAs,
  useBackups,
} from "@/components/database/home/reference"
import { StateRegion } from "@/components/database/home/state-region"
import { usePower } from "@/components/database/home/verbs"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The home of a database that is not answering.
 *
 * It is still that database's page: the identity line, what state the server
 * is in and why, the one thing to do about it, and the reference blocks that
 * need no dial — what it runs as, where it listens, the dumps that would
 * bring it back. Nothing that asks the engine a question is mounted, so a
 * stopped server is not dialled by its own home any more than by the fleet.
 */
export function DownHome() {
  const { engine, status } = useDatabase()
  // A saved connection that cannot be opened has no address to say anything
  // about: its notice is the whole page.
  const known = status.state !== "broken"
  const backups = useBackups(known && engine.can("dump"))
  return (
    <>
      <HomeIdentity />
      <StateRegion>
        <StateNotice />
      </StateRegion>
      {known && (
        <Columns>
          <RunsAs />
          {engine.can("server") && <ReachableFrom />}
          {engine.can("dump") && <BackupsBlock backups={backups} answering={false} />}
        </Columns>
      )}
    </>
  )
}

/**
 * What the server is doing instead of answering, as the one notice on the
 * page (§14: a notice is for what the reader has to act on). A server
 * somebody stopped is said plainly and offered its Start; one that refuses is
 * red, with the engine's own words; a saved connection that can no longer be
 * opened points at where it is repaired. A change begun from the dashboard
 * stands in its place for as long as it lasts (`StateRegion`).
 *
 * The summary counts the deployment environments bound to the database, so a
 * server that is down says who is without it: that is the fact that decides
 * how soon it has to be back, and it costs no dial to state.
 */
function StateNotice() {
  const { conn, summary, status, href } = useDatabase()
  const power = usePower()
  const container = summary?.container
  const unit = summary?.unit
  const where = container
    ? `Its container ${container.name}`
    : unit
      ? `Its unit ${unit.name}`
      : "The server"
  const bound = summary?.consumers ?? 0
  const without = bound > 0 && (
    <p>
      {plural(bound, "deployment environment")} {bound === 1 ? "is" : "are"} bound to it and{" "}
      {bound === 1 ? "is" : "are"} without a database until it is back.
    </p>
  )

  const start = power.can.start && (
    <Button size="sm" onClick={() => void power.run("start")}>
      <Play />
      {POWER.start.label}
    </Button>
  )
  const check = (
    <Button size="sm" variant="outline" onClick={status.refresh}>
      Check again
    </Button>
  )
  const settings = (
    <Link href={href("settings")} className="rounded-sm underline focus-ring">
      Settings
    </Link>
  )

  if (status.state === "stopped") {
    return (
      <Notice icon={Stop} title={`${conn.name} is stopped`}>
        <p>
          {where} is not running
          {container?.status ? ` — Docker says “${container.status}”` : ""}
          {!container && unit ? ` — systemd says ${unit.activeState}` : ""}. It was not dialled, so
          nothing below was asked of it.
        </p>
        {without}
        {!power.can.start && summary?.power.reason && <p>{summary.power.reason}</p>}
        <Actions>{start}</Actions>
      </Notice>
    )
  }
  if (status.state === "paused") {
    return (
      <Notice tone="warning" icon={Pause} title={`${conn.name} is paused`}>
        <p>
          {where} is frozen: its sessions are held open and nothing they ask is answered. Resume it
          from the container&rsquo;s own page; from here a paused container can only be stopped.
        </p>
        {without}
        {container && (
          <Actions>
            <Button size="sm" variant="outline" asChild>
              <Link href={`/docker/containers/${encodeURIComponent(container.name)}`}>
                Open the container
              </Link>
            </Button>
          </Actions>
        )}
      </Notice>
    )
  }
  if (status.state === "broken") {
    return (
      <Notice tone="danger" icon={Warning} title="This connection cannot be opened">
        {status.error && <p className="wrap-anywhere">{status.error}</p>}
        <p>Give it its connection string again under {settings}, or forget it there.</p>
      </Notice>
    )
  }
  if (status.state === "unreachable") {
    return (
      <Notice tone="danger" icon={Warning} title={`${conn.name} is not answering`}>
        {status.error && <p className="font-mono wrap-anywhere">{status.error}</p>}
        <p>
          Nothing says the server was stopped, and it refused or did not reply. The address and the
          password the dashboard dials it with are under {settings}.
        </p>
        {without}
        <Actions>
          {start}
          {check}
        </Actions>
      </Notice>
    )
  }
  // An engine with no home of its own, answering: there is nothing to say.
  if (status.state === "running") return null
  // The summary itself could not be read: nothing is known about the server.
  return (
    <Notice tone="warning" icon={Warning} title={`What ${conn.name} is doing could not be read`}>
      {status.error && <p className="wrap-anywhere">{status.error}</p>}
      <Actions>{check}</Actions>
    </Notice>
  )
}

function Actions({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2 pt-1.5 empty:hidden", className)}>
      {children}
    </div>
  )
}

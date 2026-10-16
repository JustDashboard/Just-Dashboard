"use client"

import { useRef } from "react"
import Link from "next/link"
import { Clock, FolderOpen } from "@/components/icons"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { BackupJob } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { useNow } from "@/components/deploy/vocabulary"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { copyTone } from "@/components/database/home/holdings"
import { EngineGlyph } from "@/components/database/kit"
import type { DbBackups } from "@/components/database/ops/backups-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** The schedule as one line: its name, then when it runs next or that it is paused. */
export function scheduleWords(schedule: BackupJob | undefined): string {
  if (!schedule) return "No scheduled backup job"
  const when = schedule.enabled
    ? schedule.nextRun
      ? `Next ${timestamp(schedule.nextRun)}`
      : "Scheduled"
    : "Paused"
  return `${schedule.name} · ${when}`
}

/**
 * Where this database's dumps go, drawn as the way they travel: the database
 * and the schedule that asks for a dump on the left, this server's dump tool
 * in the middle, and the directory they are kept in on the right.
 *
 * The line is how recent the newest dump is — green today, amber past a
 * week, still in between, dashed while there is none — and it carries a
 * pulse while a dump is being taken. The schedule's line is still while it is
 * on or paused and dashed where there is none, because a dump is then only
 * ever taken by hand. The way to the schedule is the schedule's own node.
 */
export function DumpsPicture({
  data,
  schedule,
  scheduleRead,
}: {
  data?: DbBackups
  schedule?: BackupJob
  /** The scheduled jobs have been read: until then, the schedule is not said. */
  scheduleRead: boolean
}) {
  const { id, conn, engine } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const databaseMark = useRef<HTMLDivElement>(null)
  const scheduleMark = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const folderMark = useRef<HTMLDivElement>(null)
  const files = data?.files ?? []
  const newest = files[0]
  const dumping = Boolean(data?.job)
  // The newest dump's age is read against a clock that moves, so a page left
  // open overnight does not keep calling yesterday's dump today's.
  const now = useNow(60_000, Boolean(newest))
  const age = copyTone(newest?.takenAt, now)
  const kept = files.reduce((sum, file) => sum + file.size, 0)
  const tool = [newest?.tool, newest?.toolVersion].filter(Boolean).join(" ")
  const line = {
    still: !dumping,
    dashed: !newest && !dumping,
    tone: dumping ? ("default" as const) : age,
  }

  const database = (
    <WireNode
      key="database"
      nodeRef={databaseMark}
      align="end"
      mark={
        <WireMark size="md" tone="logo">
          <EngineGlyph engine={engine} />
        </WireMark>
      }
      eyebrow={engine.label}
      title={<span className="font-mono text-xs wrap-anywhere">{conn.database || conn.name}</span>}
      hint={conn.name}
    />
  )
  const plan = (
    <WireNode
      key="schedule"
      nodeRef={scheduleMark}
      align="end"
      mark={
        schedule ? (
          <WireMark size="md" tone={schedule.enabled ? "logo" : "neutral"}>
            <Clock className={cn(schedule.enabled && "text-brand")} />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" fallback={Clock} />
        )
      }
      eyebrow="Schedule"
      title={
        scheduleRead ? (
          <span className={cn(!schedule && "text-muted-foreground")}>
            {scheduleWords(schedule)}
          </span>
        ) : (
          "…"
        )
      }
      hint={
        <Link
          href={`/backups?database=${id}`}
          className="rounded-sm underline underline-offset-2 focus-ring"
        >
          Manage backup schedule
        </Link>
      }
    />
  )
  const host = (
    <WireNode
      nodeRef={hostMark}
      align="center"
      mark={<WireHost size="md" />}
      eyebrow="This server"
      title={
        <span className="font-mono text-xs">
          {tool || (newest ? newest.format : "the dump tool")}
        </span>
      }
      hint={
        dumping ? (
          <TextShimmer>writing a dump now</TextShimmer>
        ) : newest ? (
          <span
            title={timestamp(newest.takenAt)}
            className={cn(age === "warning" && "text-warning", age === "success" && "text-success")}
          >
            last dump {relativeTime(newest.takenAt)}
          </span>
        ) : (
          "no dump written yet"
        )
      }
    />
  )
  const folder = (
    <WireNode
      nodeRef={folderMark}
      align="start"
      mark={
        files.length > 0 ? (
          <WireMark size="md" tone="logo">
            <FolderOpen />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" fallback={FolderOpen} />
        )
      }
      eyebrow="Kept in"
      title={<span className="font-mono text-xs wrap-anywhere">{data?.dir ?? "…"}</span>}
      hint={
        data
          ? files.length > 0
            ? `${plural(files.length, "dump")} · ${bytes(kept)}`
            : "empty"
          : undefined
      }
    />
  )

  return (
    <SettingPicture
      label={`Where the dumps of ${conn.name} go`}
      containerRef={container}
      className="pt-2 pb-8"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={databaseMark}
            toRef={hostMark}
            {...line}
          />
          <AnimatedBeam
            key={schedule ? "planned" : "unplanned"}
            containerRef={container}
            fromRef={scheduleMark}
            toRef={hostMark}
            still
            dashed={!schedule}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={hostMark}
            toRef={folderMark}
            delay={0.6}
            {...line}
          />
        </>
      }
      start={[database, plan]}
      middle={host}
      end={folder}
    />
  )
}

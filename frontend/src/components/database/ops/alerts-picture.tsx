"use client"

import { useRef } from "react"
import Link from "next/link"
import { Bell } from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import type { NotificationChannel } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { ChannelGlyph, channelMark } from "@/components/deploy/vocabulary"
import { toldBy } from "@/components/deploy/settings/traffic-alerts"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyphs } from "@/components/product-logo"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { EngineGlyph } from "@/components/database/kit"
import type { DbAlerts } from "@/components/database/ops/alerts-types"
import { isFiring } from "@/components/database/ops/alerts-model"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Who is told about this database, drawn as the way a message travels: the
 * database and how many rules watch it on the left, this server — which reads
 * it every thirty seconds — in the middle, and the channels the rules tell on
 * the right.
 *
 * The lines are still while every rule is quiet and red while one is firing,
 * dashed where there is nothing to carry: no rule watching, or no channel to
 * tell. The way to the channels is the channels' own node.
 */
export function AlertsPicture({
  data,
  channels,
}: {
  data?: DbAlerts
  channels: NotificationChannel[]
}) {
  const { conn, engine } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const databaseMark = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const channelsMark = useRef<HTMLDivElement>(null)
  const rules = (data?.rules ?? []).filter((rule) => rule.enabled)
  const subjects = data?.subjects ?? []
  const firingRules = rules.filter((rule) => isFiring(subjects.filter((s) => s.ruleId === rule.id)))
  const firing = firingRules.length > 0
  const reached = [
    ...new Map(
      rules.flatMap((rule) => toldBy(rule.channels, channels)).map((c) => [c.id, c]),
    ).values(),
  ]
  const tone = firing ? ("danger" as const) : ("default" as const)

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
      title={conn.name}
      hint={
        data ? (
          <span className={cn(firing && "text-destructive")}>
            {rules.length === 0
              ? "no rule watches it"
              : `${plural(rules.length, "rule")} watching${firing ? `, ${firingRules.length} firing` : ""}`}
          </span>
        ) : undefined
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
        engine.can("server")
          ? `Reads it every ${data?.everySeconds ?? 30} s`
          : `Checks its backups every ${data?.everySeconds ?? 30} s`
      }
      hint={data?.lastPass ? `checked ${relativeTime(data.lastPass)}` : "not checked yet"}
    />
  )
  const told = (
    <WireNode
      nodeRef={channelsMark}
      align="start"
      mark={
        reached.length > 0 ? (
          <WireMark size="md" tone="logo">
            <ChannelGlyph channel={reached[0]} />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" fallback={Bell} />
        )
      }
      eyebrow="Tells"
      title={
        reached.length > 0 ? (
          <span className="wrap-anywhere">{reached.map((c) => c.name).join(" · ")}</span>
        ) : (
          <span className="text-muted-foreground">
            {channels.length === 0 ? "No notification channel" : "Nobody yet"}
          </span>
        )
      }
      hint={
        reached.length > 1 ? (
          <span className="inline-flex items-center gap-1.5">
            <ProductGlyphs
              ids={reached.map(channelMark).filter((mark): mark is string => Boolean(mark))}
            />
            {plural(reached.length, "channel")}
          </span>
        ) : (
          <Link
            href="/deploy/notifications"
            className="rounded-sm underline underline-offset-2 focus-ring"
          >
            {channels.length === 0 ? "Add a channel" : "Notification channels"}
          </Link>
        )
      }
    />
  )

  return (
    <SettingPicture
      label={`Who is told about ${conn.name}`}
      containerRef={container}
      className="pt-2 pb-8"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={databaseMark}
            toRef={hostMark}
            still={!firing}
            dashed={rules.length === 0}
            tone={tone}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={hostMark}
            toRef={channelsMark}
            delay={0.6}
            still={!firing}
            dashed={rules.length === 0 || reached.length === 0}
            tone={tone}
          />
        </>
      }
      start={[database]}
      middle={host}
      end={told}
    />
  )
}

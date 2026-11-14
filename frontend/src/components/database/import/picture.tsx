"use client"

import { useRef } from "react"
import { Rss } from "@/components/icons"
import { cn } from "@/lib/utils"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireLabel, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { EngineGlyph } from "@/components/database/kit"
import { replicationPath } from "@/components/database/import/model"
import type { DbImportLive } from "@/components/database/import/types"
import { useDatabase } from "@/components/database/shell/database-context"

const TONE = { default: "", warning: "text-warning", danger: "text-destructive" } as const

/**
 * A replication as what it is: the source, the subscription that follows it,
 * and the database it fills on this server.
 *
 * The first line is the source's side of it — it moves while rows or changes
 * are still on their way, and how far behind the target is is written on it,
 * because that is a property of the link and not of either end. The second is
 * the target's: it carries while the subscription applies what it receives,
 * and breaks when it does not. Each end says what it holds: the address it is
 * read from, the tables that are in.
 *
 * It took the place of four tiles (tables copied, behind the source, applying
 * changes, errors), which gave a reader four figures to put together into
 * this.
 */
export function ReplicationPicture({ live }: { live: DbImportLive }) {
  const { engine } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const sourceMark = useRef<HTMLDivElement>(null)
  const subscriptionMark = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const imp = live.import
  const path = replicationPath(live)
  const lag = <span className={TONE[path.lag.tone]}>{path.lag.words}</span>

  return (
    <SettingPicture
      label={`How ${imp.database} follows its source`}
      containerRef={container}
      className="py-2"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={sourceMark}
            toRef={subscriptionMark}
            still={!path.arriving}
            dashed={!path.subscribed}
            tone={path.lag.tone === "warning" ? "warning" : path.settled ? "success" : "default"}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={subscriptionMark}
            toRef={hostMark}
            still={!path.arriving || !path.applying}
            dashed={!path.subscribed || !path.applying}
            tone={
              path.subscribed && !path.applying ? "warning" : path.settled ? "success" : "default"
            }
            delay={0.6}
          />
        </>
      }
      startLabel={
        path.subscribed && (
          <WireLabel lit={path.arriving} className={cn("bottom-1/2 mb-2", TONE[path.lag.tone])}>
            {path.lag.words}
          </WireLabel>
        )
      }
      start={[
        <WireNode
          key="source"
          nodeRef={sourceMark}
          align="end"
          mark={
            <WireMark size="md" tone="logo">
              <EngineGlyph engine={engine} />
            </WireMark>
          }
          eyebrow="Source"
          title={<span className="font-mono text-xs wrap-anywhere">{imp.source.address}</span>}
          hint={
            <>
              {imp.source.database && <span className="font-mono">{imp.source.database}</span>}
              {/* The line's caption is drawn from `lg` and is no part of the
                  list a screen reader hears: under that, and to it, the lag
                  is said here. */}
              {path.subscribed && (
                <span className="lg:sr-only">
                  {imp.source.database && " · "}
                  {lag}
                </span>
              )}
            </>
          }
        />,
      ]}
      middle={
        <WireNode
          nodeRef={subscriptionMark}
          align="center"
          mark={
            path.subscribed ? (
              <WireMark size="md" tone={path.applying ? "logo" : "warning"}>
                <Rss className={cn(path.applying && "text-brand")} />
              </WireMark>
            ) : (
              <WirePlaceholder size="md" fallback={Rss} />
            )
          }
          eyebrow="Subscription"
          title={<span className="font-mono text-xs wrap-anywhere">{imp.name}</span>}
          hint={
            path.subscribed ? (
              <span title={path.errors.title}>
                <span className={cn(!path.applying && "text-warning")}>
                  {path.applying ? "applying changes" : "not applying changes"}
                </span>
                {path.errors.words && (
                  <span className={TONE[path.errors.tone]}> · {path.errors.words}</span>
                )}
              </span>
            ) : (
              <TextShimmer>being made</TextShimmer>
            )
          }
        />
      }
      end={
        <WireNode
          nodeRef={hostMark}
          align="start"
          mark={<WireHost size="md" />}
          eyebrow="This server"
          title={<span className="font-mono text-xs wrap-anywhere">{imp.database}</span>}
          hint={path.copied}
        />
      }
    />
  )
}

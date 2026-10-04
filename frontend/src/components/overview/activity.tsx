"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Archive, ArrowRight, CloudUpload, Servers } from "@/components/icons"
import { ActionMark, ActionName, auditSection } from "@/components/audit/marks"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { useAuth } from "@/hooks/use-auth"
import { clock, duration, relativeTime, timestamp } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import type { MetricEvent } from "@/lib/types"
import { activityAction, activityStatus } from "./activity-event"

const SOURCES = {
  deploy: { label: "Deployments", glyph: CloudUpload, action: "deploy" },
  backup: { label: "Backups", glyph: Archive, action: "backup" },
  reboot: { label: "Server", glyph: Servers, action: "server" },
  action: { label: "Activity", glyph: Servers, action: "activity" },
}

/** The day's events carry the same product identities as the full audit trail. */
export function ActivityPanel({ events }: { events: MetricEvent[] }) {
  const { can } = useAuth()
  const newestFirst = useMemo(() => [...events].reverse(), [events])

  return (
    <Panel plain>
      <PanelHeader
        title="Recent activity"
        actions={
          // Keep the processes panel's control height so their hairlines meet.
          <div className="flex h-8 items-center gap-3 text-hint">
            <span className="text-muted-foreground">Last 24 hours</span>
            {can("system.admin") && (
              <Link
                href="/audit"
                className="flex items-center gap-1 rounded-md font-medium text-muted-foreground focus-ring transition-colors hover:text-foreground"
              >
                Audit log <ArrowRight aria-hidden className="size-3" />
              </Link>
            )}
          </div>
        }
      />
      <PanelBody
        flush
        className={
          newestFirst.length === 0 ? "py-4" : "-mx-3 max-h-[23.5rem] overflow-y-auto px-3 py-1"
        }
      >
        {newestFirst.length === 0 ? (
          <p className="text-body text-muted-foreground">Nothing in the last 24 hours.</p>
        ) : (
          <RowList aria-label="Recent server activity" className="animate-rise">
            {newestFirst.map((event, i) => (
              <ActivityRow key={`${event.ts}-${i}`} event={event} />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}

function ActivityRow({ event }: { event: MetricEvent }) {
  const action = activityAction(event)
  const section = action ? auditSection(action.action) : undefined
  const source = SOURCES[event.kind]
  const label = section?.label ?? source.label
  const hue = hueFor(section?.key ?? source.action, LANES)
  const status = activityStatus(event)

  return (
    <Row
      leading={
        <span style={{ color: hue }}>
          {action ? (
            <ActionMark action={action.action} />
          ) : (
            <ProductLogo size="sm" fallback={source.glyph} className="[&_svg]:text-inherit" />
          )}
        </span>
      }
      title={
        <span title={event.title}>
          {action ? (
            <>
              {action.label ?? <ActionName action={action.action} />}
              {action.target && (
                <span className="ml-2 font-mono font-normal text-muted-foreground">
                  {action.target}
                </span>
              )}
            </>
          ) : (
            event.title
          )}
        </span>
      }
      subtitle={
        <span title={event.detail}>
          <span style={{ color: hue }}>{label}</span>
          {" · "}
          {relativeTime(event.ts)}
          {event.detail && ` · ${event.detail}`}
          {!!event.durationSeconds && ` · ${duration(event.durationSeconds)}`}
        </span>
      }
      trailing={
        <span className="flex flex-col items-end gap-1">
          <Status tone={status.tone} label={status.label} className="text-hint" />
          <time
            dateTime={event.ts}
            title={timestamp(event.ts)}
            className="numeric text-hint text-muted-foreground"
          >
            {clock(event.ts)}
          </time>
        </span>
      }
      className="py-2.5"
    />
  )
}

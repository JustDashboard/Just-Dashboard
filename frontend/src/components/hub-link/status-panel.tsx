"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Archive,
  ArrowRight,
  ArrowUpRight,
  Heart,
  RefreshClockwise,
  Warning,
} from "@/components/icons"
import { commandWords } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { hubHost, hubSays, noticeHref, noticeTone } from "@/lib/hub-link"
import { notify } from "@/lib/toast"
import type { HubLinkNotice, HubLinkStatus } from "@/lib/types"
import { MarkTile, type Mark } from "@/components/account-link/marks"
import { useConfirm } from "@/components/confirm-dialog"
import { Section } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

type LinkState = NonNullable<NonNullable<HubLinkStatus["admin"]>["link"]>

/** On the section's own edge, under its title: the rows open nothing, so they take no bleed. */
const ROW = "px-0 py-2.5"

const COMMAND_STATUS = {
  succeeded: { tone: "running", label: "Succeeded" },
  failed: { tone: "danger", label: "Failed" },
  refused: { tone: "warning", label: "Refused" },
} as const

/** A command drawn as what it acts on, as Settings → Connect draws the site's. */
function commandMark(kind: string): Mark {
  if (kind.startsWith("dashboard.")) return { brand: true }
  if (kind.startsWith("container.")) return { product: "docker" }
  if (kind.startsWith("stack.")) return { product: "docker-compose" }
  if (kind.startsWith("backup.")) return { glyph: Archive, hue: "var(--tag-violet)" }
  return { glyph: RefreshClockwise, hue: "var(--tag-blue)" }
}

/** Who the hub says asked; "rollout" is a step of an update rollout, not a person. */
function askedBy(issuedBy: string) {
  return issuedBy === "rollout" ? "an update rollout" : issuedBy
}

/**
 * What the link has been doing: the report this server sends, and the last thing the hub ran
 * here, each with its state at the far end. Every command is in the audit log under
 * `hub.command.*` with the hub as its actor, so the section leads there for the rest.
 */
export function HubActivity({
  status,
  link,
  host,
  className,
}: {
  status: HubLinkStatus
  link: LinkState
  host: string
  className?: string
}) {
  const command = link.lastCommand
  const unreachable = status.reachable === false

  return (
    <Section
      title="Activity"
      className={className}
      actions={
        <Button asChild size="xs" variant="ghost" className="text-muted-foreground">
          <Link href="/audit?action=hub.command.">
            Every command in the audit log
            <ArrowRight />
          </Link>
        </Button>
      }
    >
      {link.lastError && (
        <Notice tone="warning" icon={Warning} title={`The last exchange with ${host} failed`}>
          {link.lastError}
        </Notice>
      )}
      <RowList>
        <Row
          className={ROW}
          leading={<MarkTile mark={{ glyph: Heart, hue: "var(--tag-green)" }} />}
          title="Report"
          subtitle={
            status.lastHeartbeatAt
              ? `Last sent ${relativeTime(status.lastHeartbeatAt)} · ${timestamp(status.lastHeartbeatAt)}`
              : "Sent every 15 seconds"
          }
          trailing={
            unreachable ? (
              <Status tone="warning" label="Not reaching the hub" />
            ) : status.lastHeartbeatAt ? (
              <Status tone="running" label="Sending" live />
            ) : (
              <Status tone="stopped" label="Not yet" />
            )
          }
        />
        {command ? (
          <Row
            className={ROW}
            leading={<MarkTile mark={commandMark(command.kind)} />}
            title={commandWords(command.kind)}
            subtitle={`Last command · asked by ${askedBy(command.issuedBy)} · ${relativeTime(command.at)}`}
            trailing={
              <Status
                tone={COMMAND_STATUS[command.status].tone}
                label={COMMAND_STATUS[command.status].label}
              />
            }
          />
        ) : (
          <Row
            className={ROW}
            leading={<MarkTile mark={{ glyph: RefreshClockwise, hue: "var(--tag-blue)" }} />}
            title="Nothing run yet"
            subtitle={`What ${host} runs on this server shows up here`}
          />
        )}
      </RowList>
    </Section>
  )
}

/**
 * Everything the hub is saying, newest first: the rail's card holds only the newest one this
 * browser has not dismissed, and its "more" leads here. Each one is plain text from another
 * machine, drawn as text under who says it (hub-link-protocol §8.10), with its link naming the
 * host it opens.
 */
export function HubNotices({
  notices,
  hubUrl,
  hubName,
  className,
}: {
  notices: HubLinkNotice[]
  hubUrl?: string
  /** The hub's name, its `owner.label`, which every notice is attributed to. */
  hubName?: string
  className?: string
}) {
  return (
    <div id="notices" className={cn("scroll-mt-6", className)}>
      <Section title="From Just Hub">
        <div className="flex min-w-0 flex-col gap-2">
          {notices.map((notice) => {
            const href = noticeHref(notice.href, hubUrl)
            return (
              <Notice
                key={notice.id}
                tone={noticeTone(notice.level)}
                title={
                  <>
                    <span className="block text-hint font-normal text-muted-foreground">
                      {hubSays(hubName)}
                    </span>
                    {notice.title}
                  </>
                }
              >
                {notice.body && <p className="break-words">{notice.body}</p>}
                <p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span>{relativeTime(notice.at)}</span>
                  {href && (
                    <a
                      href={href}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
                    >
                      Open in Just Hub
                      <span className="font-normal text-muted-foreground">{hubHost(href)}</span>
                      <ArrowUpRight aria-hidden className="size-3" />
                    </a>
                  )}
                </p>
              </Notice>
            )
          })}
        </div>
      </Section>
    </div>
  )
}

/** Sync now and Disconnect, for the identity line: the two things done to a link that exists. */
export function HubLinkActions({
  host,
  name,
  onSync,
  onDisconnect,
}: {
  host: string
  name: string
  onSync: () => Promise<HubLinkStatus>
  onDisconnect: () => Promise<void>
}) {
  const { confirm, dialog } = useConfirm()
  const [syncing, setSyncing] = useState(false)

  const sync = async () => {
    setSyncing(true)
    try {
      const next = await onSync()
      if (next.reachable === false)
        notify.error(`Could not reach ${host}`, next.admin?.link?.lastError)
      else notify.success(`Report sent to ${host}`)
    } catch (err) {
      notify.error("Could not sync", errorMessage(err))
    } finally {
      setSyncing(false)
    }
  }

  const askDisconnect = () =>
    confirm({
      title: "Disconnect from Just Hub",
      confirmLabel: "Disconnect",
      workingLabel: "Disconnecting…",
      description: (
        <>
          <p>
            This server stops reporting to {host} and ignores every command from it. The hub is
            told, and if it cannot be reached the link is cleared here anyway.
          </p>
          <p className="text-muted-foreground">
            Connected to {name}. This server keeps its key, so connecting again shows the same
            fingerprint.
          </p>
        </>
      ),
      action: async () => {
        await onDisconnect()
        notify.success(`Disconnected from ${host}`)
        return "reported"
      },
    })

  return (
    <>
      {dialog}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onClick={sync} pending={syncing}>
          <RefreshClockwise />
          Sync now
        </Button>
        <Button variant="outline" size="sm" className="text-destructive" onClick={askDisconnect}>
          Disconnect
        </Button>
      </div>
    </>
  )
}

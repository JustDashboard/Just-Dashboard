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
import {
  exchangeFailure,
  hubHost,
  hubSays,
  noticeHref,
  noticeTone,
  reportCadence,
} from "@/lib/hub-link"
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
 * `hub.command.*` with the hub as its actor, so the section leads there for the rest. An idle
 * link's last error is its reason, which the page already says above everything, so here the
 * report only reads as not sent. Any other last error is said as what happened
 * (`exchangeFailure`), with Go's own words for a request that got no answer behind a disclosure.
 */
export function HubActivity({
  status,
  link,
  host,
  idle,
  className,
}: {
  status: HubLinkStatus
  link: LinkState
  host: string
  /** This server has stopped using the link (`idleReason`). */
  idle?: boolean
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
      {link.lastError && !idle && (
        <Notice tone="warning" icon={Warning} title={`The last exchange with ${host} failed`}>
          <ExchangeFailure error={link.lastError} host={host} />
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
              : `Sent ${reportCadence(status.heartbeatSeconds)}`
          }
          trailing={
            idle ? (
              <Status tone="danger" label="Not sent" />
            ) : unreachable ? (
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

function ExchangeFailure({ error, host }: { error: string; host: string }) {
  const { message, raw } = exchangeFailure(error, host)
  return (
    <>
      <p className="break-words">{message}</p>
      {raw && (
        <details data-slot="hub-link-raw-error" className="mt-1 min-w-0">
          <summary className="cursor-pointer hover:text-foreground">
            What this server recorded
          </summary>
          <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
            {raw}
          </pre>
        </details>
      )}
    </>
  )
}

/**
 * Everything the hub is saying, newest first: the rail's card holds only the newest one this
 * browser has not dismissed, and its "more" leads here. Each one is plain text from another
 * machine, drawn as text under who says it (hub-link-protocol §8.10), with its link naming the
 * host it opens. Without `hubUrl` — an idle link — no notice links anywhere.
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

/**
 * Sync now and Disconnect, for the identity line: the two things done to a link that exists. An
 * idle link offers only Disconnect: a sync would be refused (`409 hub_url_refused`), and the hub
 * is not told of the disconnect, since this server no longer sends it anything. The link is
 * cleared here whether or not the hub heard, so afterwards the page says which it was: a hub that
 * was not told still lists this server until it is removed there.
 */
export function HubLinkActions({
  host,
  name,
  idle,
  onSync,
  onDisconnect,
}: {
  host: string
  /** "Just Hub (hub)", or the host before the hub has a name. */
  name: string
  idle?: boolean
  onSync: () => Promise<HubLinkStatus>
  /** Disconnects, and answers whether the hub was told. */
  onDisconnect: () => Promise<boolean>
}) {
  const { confirm, dialog } = useConfirm()
  const [syncing, setSyncing] = useState(false)

  const sync = async () => {
    setSyncing(true)
    try {
      const next = await onSync()
      const failure = next.admin?.link?.lastError
      if (next.reachable === false)
        notify.error(
          `Report not sent to ${host}`,
          failure ? exchangeFailure(failure, host).message : undefined,
        )
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
          {idle ? (
            <p>
              This server forgets its idle link to {host}. It sends nothing there now, so the hub is
              not told, and lists this server until it is removed there.
            </p>
          ) : (
            <p>
              This server stops reporting to {host} and ignores every command from it. The hub is
              told, and if it cannot be reached the link is cleared here anyway.
            </p>
          )}
          <p className="text-muted-foreground">
            Connected to {name}. This server keeps its key, so connecting again shows the same
            fingerprint.
          </p>
        </>
      ),
      action: async () => {
        if (await onDisconnect()) notify.success(`Disconnected from ${host}`)
        else
          notify.warning("Disconnected here", {
            description: `${host} ${idle ? "was not told" : "could not be told"}, and still lists this server until it is removed there.`,
            duration: 12_000,
          })
        return "reported"
      },
    })

  return (
    <>
      {dialog}
      <div className="flex flex-wrap items-center gap-2">
        {!idle && (
          <Button variant="outline" size="sm" onClick={sync} pending={syncing}>
            <RefreshClockwise />
            Sync now
          </Button>
        )}
        <Button variant="outline" size="sm" className="text-destructive" onClick={askDisconnect}>
          Disconnect
        </Button>
      </div>
    </>
  )
}

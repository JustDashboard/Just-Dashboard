"use client"

import { useState } from "react"
import { Archive, Heart, RefreshClockwise, Warning } from "@/components/icons"
import { commandWords, heartbeatStale, scopeName } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { calendarDate, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { useNow } from "@/hooks/use-now"
import { MarkTile, type Mark } from "@/components/account-link/marks"
import { Reading, ReadingList } from "@/components/account-link/reading"
import { ScopeCards } from "@/components/account-link/scope-cards"
import { useConfirm } from "@/components/confirm-dialog"
import { Section } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

type Link = NonNullable<AccountLinkStatus["link"]>

/** On the section's own edge, under its title: the rows open nothing, so they take no bleed. */
const ROW = "px-0 py-3.5"

const COMMAND_STATUS = {
  succeeded: { tone: "running", label: "Succeeded" },
  failed: { tone: "danger", label: "Failed" },
  refused: { tone: "warning", label: "Refused" },
} as const

/** A command drawn as what it acts on: Docker for a container or a stack, this dashboard itself. */
function commandMark(kind: string): Mark {
  if (kind.startsWith("dashboard.")) return { brand: true }
  if (kind.startsWith("container.")) return { product: "docker" }
  if (kind.startsWith("stack.")) return { product: "docker-compose" }
  if (kind.startsWith("backup.")) return { glyph: Archive, hue: "var(--tag-violet)" }
  return { glyph: RefreshClockwise, hue: "var(--tag-blue)" }
}

/**
 * What the link has been doing: the heartbeat this server sends, and the last thing the site
 * asked it to do. They were two of three figures in a row of tiles, with the third — when the link
 * was made — already the identity line's; as rows they carry the state as a `Status` at their far
 * end, which is where every other page in the product says whether a thing is working.
 */
export function Activity({
  link,
  site,
  className,
}: {
  link: Link
  site: string
  className?: string
}) {
  const now = useNow(10_000)
  const stale = heartbeatStale(link.lastHeartbeatAt, now)
  const command = link.lastCommand
  const sender = command?.issuedBy.replace(/^account:/, "")

  return (
    <Section title="Activity" className={className}>
      {link.lastError && (
        <Notice tone="warning" icon={Warning} title={`The last exchange with ${site} failed`}>
          {link.lastError}
        </Notice>
      )}
      <RowList>
        <Row
          className={ROW}
          leading={<MarkTile mark={{ glyph: Heart, hue: "var(--tag-green)" }} />}
          title="Heartbeat"
          subtitle={
            link.lastHeartbeatAt
              ? `Last sent ${relativeTime(link.lastHeartbeatAt)} · ${timestamp(link.lastHeartbeatAt)}`
              : "Sent every 30 seconds"
          }
          trailing={
            link.lastError ? (
              <Status tone="warning" label="Not reaching the site" />
            ) : stale ? (
              <Status tone="warning" label="Gone quiet" />
            ) : link.lastHeartbeatAt ? (
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
            subtitle={`Last command · ${sender} · ${relativeTime(command.at)}`}
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
            title="No commands yet"
            subtitle={`What ${site} asks this server to do shows up here`}
          />
        )}
      </RowList>
    </Section>
  )
}

/** The scope in force, as the same cards that chose it, and the command that changes it. */
export function ScopeSection({
  scope,
  onChange,
  className,
}: {
  scope: AccountLinkScope
  onChange: (scope: AccountLinkScope) => Promise<void>
  className?: string
}) {
  // Null follows the server, so a scope changed from the terminal or the site shows up here.
  const [pick, setPick] = useState<AccountLinkScope | null>(null)
  const [busy, setBusy] = useState(false)
  const draft = pick ?? scope

  const apply = async () => {
    setBusy(true)
    try {
      await onChange(draft)
      setPick(null)
      notify.success(`Scope is now ${scopeName(draft)}`)
    } catch (err) {
      notify.error("Could not change the scope", errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section
      title="Scope"
      className={className}
      actions={<span className="text-xs text-muted-foreground">Now {scopeName(scope)}</span>}
    >
      <ScopeCards value={draft} onChange={setPick} disabled={busy} />
      {draft !== scope && (
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={apply} pending={busy}>
            Change to {scopeName(draft)}
          </Button>
          <Button variant="ghost" onClick={() => setPick(null)} disabled={busy}>
            Discard
          </Button>
        </div>
      )}
    </Section>
  )
}

/** This server's key and the site keys it has pinned, so a person can compare them with the site's. */
export function Keys({
  fingerprint,
  siteKeys,
  className,
}: {
  fingerprint?: string
  siteKeys: Link["siteKeys"]
  className?: string
}) {
  return (
    <Section title="Keys" className={className}>
      <ReadingList>
        {fingerprint && (
          <Reading label="This server" mono>
            {fingerprint}
          </Reading>
        )}
        {siteKeys.map((key) => (
          <Reading key={key.kid} label="Site key" mono>
            {key.fingerprint}
            <span className="block text-hint text-muted-foreground">
              {key.notAfter ? `${key.kid} · valid until ${calendarDate(key.notAfter)}` : key.kid}
            </span>
          </Reading>
        ))}
      </ReadingList>
    </Section>
  )
}

/** Sync now and Unlink, for the identity line: the two things done to a link that exists. */
export function LinkActions({
  site,
  owner,
  onSync,
  onUnlink,
}: {
  site: string
  owner: string
  onSync: () => Promise<AccountLinkStatus>
  onUnlink: () => Promise<void>
}) {
  const { confirm, dialog } = useConfirm()
  const [syncing, setSyncing] = useState(false)

  const sync = async () => {
    setSyncing(true)
    try {
      const next = await onSync()
      if (next.link?.lastError) notify.error(`Could not reach ${site}`, next.link.lastError)
      else notify.success(`Heartbeat sent to ${site}`)
    } catch (err) {
      notify.error("Could not sync", errorMessage(err))
    } finally {
      setSyncing(false)
    }
  }

  const askUnlink = () =>
    confirm({
      title: "Unlink this server",
      confirmLabel: "Unlink",
      description: (
        <>
          <p>This server stops sending snapshots to {site} and ignores every command from it.</p>
          <p className="text-muted-foreground">
            Linked to {owner}. To use the site with this server again, link it again.
          </p>
        </>
      ),
      action: async () => {
        await onUnlink()
        notify.success(`Unlinked from ${site}`)
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
        <Button variant="outline" size="sm" className="text-destructive" onClick={askUnlink}>
          Unlink
        </Button>
      </div>
    </>
  )
}

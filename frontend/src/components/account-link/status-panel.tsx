"use client"

import { useState } from "react"
import { RefreshClockwise, Warning } from "@/components/icons"
import { heartbeatStale, scopeName } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { calendarDate, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { useNow } from "@/hooks/use-now"
import { Reading, ReadingList } from "@/components/account-link/reading"
import { ScopeCards } from "@/components/account-link/scope-cards"
import { useConfirm } from "@/components/confirm-dialog"
import { Section } from "@/components/page"
import { Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"

type Link = NonNullable<AccountLinkStatus["link"]>

/**
 * How the link is doing — when it was made, when the site last heard from this server, what it
 * last asked for — then the one setting that can change, the scope.
 *
 * Each figure is a reading of the link rather than of the machine, and the two that can go wrong
 * say so through their tone: a heartbeat that has gone quiet, a command the server refused.
 */
export function StatusPanel({
  link,
  site,
  onChangeScope,
}: {
  link: Link
  site: string
  onChangeScope: (scope: AccountLinkScope) => Promise<void>
}) {
  const now = useNow(10_000)
  const silent = Boolean(link.lastError) || heartbeatStale(link.lastHeartbeatAt, now)
  const command = link.lastCommand

  return (
    <>
      <StatGrid columns={3}>
        <StatTile
          label="Linked"
          value={relativeTime(link.linkedAt)}
          hint={link.linkedBy === "cli" ? "from the terminal" : `by ${link.linkedBy}`}
        />
        <StatTile
          label="Last heartbeat"
          value={link.lastHeartbeatAt ? relativeTime(link.lastHeartbeatAt) : "Not yet"}
          tone={silent ? "warning" : "default"}
          hint={link.lastHeartbeatAt ? timestamp(link.lastHeartbeatAt) : "Sent every 30 seconds"}
        />
        <StatTile
          label="Last command"
          value={command?.kind ?? "None"}
          tone={
            command?.status === "failed"
              ? "danger"
              : command?.status === "refused"
                ? "warning"
                : "default"
          }
          hint={command && `${command.status} · ${command.issuedBy} · ${relativeTime(command.at)}`}
        />
      </StatGrid>

      {link.lastError && (
        <Notice tone="warning" icon={Warning} title={`The last exchange with ${site} failed`}>
          {link.lastError}
        </Notice>
      )}

      <ScopeSection scope={link.scope} onChange={onChangeScope} />
    </>
  )
}

function ScopeSection({
  scope,
  onChange,
}: {
  scope: AccountLinkScope
  onChange: (scope: AccountLinkScope) => Promise<void>
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
      actions={<span className="text-hint text-muted-foreground">now {scopeName(scope)}</span>}
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
}: {
  fingerprint?: string
  siteKeys: Link["siteKeys"]
}) {
  return (
    <Section title="Keys">
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

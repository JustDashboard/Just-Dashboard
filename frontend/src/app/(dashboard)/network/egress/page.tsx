"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import {
  egressDraftFromGroup,
  emptyEgressDraft,
  type EgressDraft,
  type EgressGroup,
  type EgressView,
} from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { EgressGroupPanel } from "@/components/network/egress/egress-group"
import { EgressGroupForm } from "@/components/network/egress/egress-form"
import { Button } from "@/components/ui/button"
import { Lifebuoy, Plus } from "@/components/icons"

/**
 * Monitored egress groups: a reading page. It opens on how many groups exist,
 * how many carry traffic, how many switch on their own, and how many members
 * are proven healthy now; then each group as its own reading — what it
 * carries, every member measured through its own path, the decisions with
 * the evidence behind them and the simulation automation waits on.
 *
 * The page polls every five seconds while a simulation runs, so its progress
 * and result arrive without a reload, and every fifteen otherwise.
 */
export default function NetworkEgressPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const [running, setRunning] = useState(false)
  const read = usePoll<EgressView>(
    async (signal) => {
      const view = await get<EgressView>("/network/egress", undefined, signal)
      setRunning(Boolean(view.running))
      return view
    },
    running ? 5_000 : 15_000,
  )
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 60_000)
  const [form, setForm] = useState<{ draft: EgressDraft; editing?: EgressGroup; key: number }>()

  if (!read.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="Egress groups" />
        {read.error ? (
          <ErrorState error={read.error} onRetry={read.refresh} />
        ) : (
          <LoadingPanel plain />
        )}
      </Page>
    )
  }
  const data = read.data
  const members = data.groups.flatMap((g) => g.members)
  const proven = members.filter((m) => m.proven).length
  const down = members.filter((m) => m.state === "down").length
  const create = () => setForm({ draft: emptyEgressDraft(), key: Date.now() })
  return (
    <Page className="animate-rise">
      <PageContext
        eyebrow="Network"
        title="Egress groups"
        actions={
          <Button
            size="sm"
            variant="outline"
            disabled={!admin || data.groups.length >= data.capacity}
            onClick={create}
          >
            <Plus aria-hidden />
            New group
          </Button>
        }
      />
      <NetworkReadWarning
        error={read.error}
        refresh={read.refresh}
        lastSuccess={read.lastSuccess}
        reading="egress readings"
      />
      {!data.persistent && (
        <Notice tone="warning" title="Decisions are kept in memory only">
          The dashboard&rsquo;s database is not available to this module, so the decision record and
          sample history end with this process.
        </Notice>
      )}
      <StatGrid columns={4} dense>
        <StatTile label="Groups" value={data.groups.length} trailing={`of ${data.capacity}`} />
        <StatTile label="Carrying traffic" value={data.groups.filter((g) => g.enabled).length} />
        <StatTile label="Automated" value={data.groups.filter((g) => g.automation).length} />
        <StatTile
          label="Members proven"
          value={members.length ? `${proven}/${members.length}` : "—"}
          tone={down > 0 ? "danger" : proven < members.length ? "warning" : "default"}
        />
      </StatGrid>
      {data.groups.length === 0 ? (
        <EmptyState
          icon={Lifebuoy}
          title="No egress groups"
          description="A group measures several gateways or owned tunnels through their own paths and moves selected traffic between them by latency, loss and failure, with every decision recorded. It carries nothing until enabled, and switches on its own only after a simulation."
          action={
            admin ? (
              <Button size="sm" variant="outline" onClick={create}>
                <Plus aria-hidden />
                New group
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="flex flex-col gap-10">
          {data.groups.map((group) => (
            <EgressGroupPanel
              key={group.id}
              group={group}
              running={data.running}
              onChanged={read.refresh}
              onEdit={(g) =>
                setForm({ draft: egressDraftFromGroup(g), editing: g, key: Date.now() })
              }
            />
          ))}
        </div>
      )}
      {form && (
        <EgressGroupForm
          key={form.key}
          open
          onOpenChange={(open) => !open && setForm(undefined)}
          initial={form.draft}
          editing={form.editing}
          links={links.data ?? []}
          onSaved={read.refresh}
        />
      )}
    </Page>
  )
}

"use client"

import Link from "next/link"
import { useState } from "react"
import { Hash, Plus, RefreshClockwise } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { IdentityFacts, IdentityStateLoading } from "@/components/network/identity-facts"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  ipamCounts,
  readIPAMView,
  type IPAMPool,
  type IPAMPreview,
  type IPAMReservation,
  type IPAMView,
} from "@/lib/network-ipam"
import { IPAMPools } from "@/components/network/ipam/ipam-pools"
import { IPAMPreviewField } from "@/components/network/ipam/ipam-preview"
import { IPAMCoverageTable, IPAMReservations } from "@/components/network/ipam/ipam-reservations"
import {
  PoolSheet,
  ReserveSheet,
  newPoolDraft,
  newReserveDraft,
  type ReserveDraft,
} from "@/components/network/ipam/ipam-sheets"

type Sheet = "pool" | "reserve" | null

/**
 * Reading register: the address space set aside for planning, what is held
 * from it and for whom, and where the evidence stops.
 *
 * The page called itself "Shared address pools" under a rail that says
 * Address planning, and opened on four tiles that counted the lists under
 * them. It is named as the rail names it; the counts are the identity line's
 * facts; a pool is a card with one band of planned, observed and free blocks
 * and its owners as logos; reservations are cards; making a pool and
 * reserving a block are sheets. The planner never presents an unread native
 * range as free: the band says "free" only of what could be read, and the
 * coverage table under it says what could not.
 */
export function AddressPlanningPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = can("destructive")
  const view = usePoll<IPAMView>(
    async (signal) => readIPAMView(await get("/network/ipam/", undefined, signal)),
    30000,
    [admin],
    { enabled: admin },
  )
  const { confirm, dialog } = useConfirm()
  const [sheet, setSheet] = useState<Sheet>(null)
  const [poolDraft, setPoolDraft] = useState(newPoolDraft)
  const [reserveDraft, setReserveDraft] = useState<ReserveDraft>(() => newReserveDraft())
  const [candidate, setCandidate] = useState("")
  const [preview, setPreview] = useState<IPAMPreview>()
  const [busy, setBusy] = useState<"preview" | "pool" | "reserve" | null>(null)
  const [error, setError] = useState<{ where: "preview" | "pool" | "reserve"; message: string }>()

  const data = view.data
  const loading = admin && !data && !view.error
  const counts = ipamCounts(data)
  const pools = data?.pools.filter((pool) => !pool.retiredAt) ?? []
  const gaps = data?.inventory.coverage.filter((row) => row.state !== "observed").length ?? 0

  const perform = async (where: "preview" | "pool" | "reserve", action: () => Promise<void>) => {
    if (busy || !admin) return
    setBusy(where)
    setError(undefined)
    try {
      await action()
    } catch (err) {
      setError({ where, message: errorMessage(err) })
    } finally {
      setBusy(null)
    }
  }
  const open = (next: Sheet) => {
    setError(undefined)
    // A reservation starts on a pool that still hands out blocks.
    if (next === "reserve" && !pools.some((pool) => pool.id === reserveDraft.poolId))
      setReserveDraft({ ...reserveDraft, poolId: pools[0]?.id ?? "" })
    setSheet(next)
  }
  const release = (row: IPAMReservation) =>
    confirm({
      title: "Release planning reservation",
      description: `Release ${row.prefix} for ${row.resource} from the shared planner. This does not remove or reconfigure its native resource. Check its native owner before reusing the range.`,
      confirmLabel: "Release plan",
      action: async () => {
        await del(`/network/ipam/reservations/${row.id}`)
        view.refresh()
      },
    })
  const retire = (pool: IPAMPool) =>
    confirm({
      title: "Retire planning pool",
      description: `Stop new planning allocations from ${pool.name}. Active reservations must be released first. Native configuration remains with its existing owner.`,
      confirmLabel: "Retire pool",
      action: async () => {
        await del(`/network/ipam/pools/${pool.id}`)
        view.refresh()
      },
    })

  const menu: Verb[] = [
    ...(pools.length > 0
      ? [{ key: "pool", label: "New pool", icon: Plus, run: () => open("pool") }]
      : []),
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: view.refresh },
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Address planning" />
      <HostIdentity
        fallback={Hash}
        title="Address planning"
        label="About address planning"
        state={
          !admin ? undefined : loading ? (
            <IdentityStateLoading />
          ) : !data ? undefined : view.error ? (
            <Status tone="warning" label="Last known plan" />
          ) : gaps > 0 ? (
            <Status tone="warning" label={`${gaps} coverage ${gaps === 1 ? "gap" : "gaps"}`} />
          ) : data.inventory.coverage.length === 0 ? (
            // Nothing reported is not the same as everything read.
            <Status tone="unknown" label="No sources reported" />
          ) : (
            <Status tone="running" label="Every source read" />
          )
        }
        facts={
          <IdentityFacts loading={loading}>
            {!admin ? (
              <span>needs system administrator access</span>
            ) : !data ? (
              <span>the plan could not be read</span>
            ) : (
              <>
                <span className="numeric">{plural(counts.pools, "pool")}</span>
                <FactDot />
                <span className="numeric">{plural(counts.reservations, "reservation")} held</span>
                <FactDot />
                <span className="numeric">{plural(counts.observed, "native range")} observed</span>
                <FactDot />
                <span>
                  read{" "}
                  <time dateTime={data.inventory.checkedAt}>
                    {relativeTime(data.inventory.checkedAt)}
                  </time>
                </span>
              </>
            )}
          </IdentityFacts>
        }
        command={
          // With no pool to reserve from, the pools' own empty state holds the next step.
          admin && data && pools.length > 0 ? (
            <Button size="sm" disabled={Boolean(view.error)} onClick={() => open("reserve")}>
              <Plus className="size-3.5" />
              Reserve
            </Button>
          ) : undefined
        }
        menu={admin && data ? <VerbMenu verbs={menu} label="More planning actions" /> : undefined}
      />
      {!admin ? (
        <EmptyState
          icon={Hash}
          title="Address planning needs an administrator"
          description="Shared pools and private native-owner inventories require system administrator access. The local subnet calculator remains available to read accounts."
          action={
            <Button asChild size="sm" variant="outline">
              <Link href="/network/tools?tool=subnet">Open the subnet calculator</Link>
            </Button>
          }
        />
      ) : !data && view.error ? (
        <ErrorState error={view.error} onRetry={view.refresh} retryable />
      ) : (
        <>
          <NetworkReadWarning
            error={view.error}
            refresh={view.refresh}
            lastSuccess={view.lastSuccess}
            reading="IPAM evidence"
          />
          <IPAMPools
            view={data}
            loading={loading}
            canCreate={admin}
            canRetire={destructive}
            busy={busy !== null}
            onCreate={() => open("pool")}
            onRetire={retire}
          >
            <IPAMPreviewField
              value={candidate}
              onChange={setCandidate}
              preview={preview}
              busy={busy === "preview"}
              error={error?.where === "preview" ? error.message : undefined}
              onPreview={() =>
                void perform("preview", async () =>
                  setPreview(
                    await post<IPAMPreview>("/network/ipam/preview", { prefix: candidate.trim() }),
                  ),
                )
              }
              onDismiss={() => setPreview(undefined)}
            />
          </IPAMPools>
          <IPAMReservations
            reservations={data?.reservations ?? []}
            loading={loading}
            canRelease={destructive}
            busy={busy !== null}
            onRelease={release}
          />
          <IPAMCoverageTable
            coverage={data?.inventory.coverage ?? []}
            limitations={data?.limitations ?? []}
            loading={loading}
          />
        </>
      )}
      <PoolSheet
        open={sheet === "pool"}
        onOpenChange={(next) => !next && setSheet(null)}
        draft={poolDraft}
        onDraft={setPoolDraft}
        busy={busy === "pool"}
        error={error?.where === "pool" ? error.message : undefined}
        onCreate={() =>
          void perform("pool", async () => {
            const created = await post<IPAMPool>("/network/ipam/pools", {
              name: poolDraft.name.trim(),
              prefix: poolDraft.prefix.trim(),
              allocationBits: Number(poolDraft.bits),
            })
            // The next reservation is most likely from the pool just made.
            setReserveDraft((current) => ({ ...current, poolId: created.id }))
            setPoolDraft(newPoolDraft())
            setSheet(null)
            view.refresh()
          })
        }
      />
      <ReserveSheet
        open={sheet === "reserve"}
        onOpenChange={(next) => !next && setSheet(null)}
        pools={pools}
        draft={reserveDraft}
        onDraft={setReserveDraft}
        gaps={gaps}
        stale={Boolean(view.error)}
        busy={busy === "reserve"}
        error={error?.where === "reserve" ? error.message : undefined}
        onReserve={() =>
          void perform("reserve", async () => {
            await post<IPAMReservation>("/network/ipam/reservations", {
              poolId: reserveDraft.poolId,
              prefix: reserveDraft.exact.trim() || undefined,
              owner: reserveDraft.owner,
              resource: reserveDraft.resource.trim(),
              acknowledgeUnknown: reserveDraft.acknowledge,
            })
            setReserveDraft(newReserveDraft(reserveDraft.poolId))
            setSheet(null)
            view.refresh()
          })
        }
      />
      {dialog}
    </Page>
  )
}

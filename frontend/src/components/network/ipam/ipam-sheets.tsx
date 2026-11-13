"use client"

import { Bridge, Globe, Layers } from "@/components/icons"
import { ChoiceCard, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ownerLabel, type IPAMOwner, type IPAMPool } from "@/lib/network-ipam"

export type PoolDraft = { name: string; prefix: string; bits: string }
export const newPoolDraft = (): PoolDraft => ({ name: "", prefix: "", bits: "24" })
export const poolDraftReady = (draft: PoolDraft) =>
  Boolean(draft.name.trim() && draft.prefix.trim() && /^\d{1,3}$/.test(draft.bits))

export function PoolSheet({
  open,
  onOpenChange,
  draft,
  onDraft,
  busy,
  error,
  onCreate,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  draft: PoolDraft
  onDraft: (draft: PoolDraft) => void
  busy: boolean
  error?: string
  onCreate: () => void
}) {
  const ready = poolDraftReady(draft)
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="New pool"
      description="Set a network aside for planning."
      width="md"
      footer={
        <>
          <FormNote className="mr-auto">Planning only. Nothing is written to the host.</FormNote>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="ipam-pool" pending={busy} disabled={!ready || busy}>
            Create pool
          </Button>
        </>
      }
    >
      <form
        id="ipam-pool"
        className="space-y-6"
        onSubmit={(event) => {
          event.preventDefault()
          if (ready && !busy) onCreate()
        }}
      >
        <Field label="Pool name" htmlFor="ipam-pool-name">
          <Input
            id="ipam-pool-name"
            value={draft.name}
            onChange={(event) => onDraft({ ...draft, name: event.target.value })}
            disabled={busy}
            maxLength={80}
          />
        </Field>
        <FieldRow>
          <Field label="Pool network" htmlFor="ipam-pool-prefix">
            <Input
              id="ipam-pool-prefix"
              value={draft.prefix}
              onChange={(event) => onDraft({ ...draft, prefix: event.target.value })}
              className="font-mono"
              placeholder="10.240.0.0/16"
              disabled={busy}
              spellCheck={false}
            />
          </Field>
          <Field
            label="Allocation prefix length"
            htmlFor="ipam-allocation-bits"
            hint="/24 blocks from an IPv4 /16, /64 from an IPv6 /48."
          >
            <Input
              id="ipam-allocation-bits"
              value={draft.bits}
              onChange={(event) => onDraft({ ...draft, bits: event.target.value })}
              className="font-mono"
              inputMode="numeric"
              disabled={busy}
            />
          </Field>
        </FieldRow>
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </form>
    </SidePanel>
  )
}

export type ReserveDraft = {
  poolId: string
  owner: IPAMOwner
  resource: string
  exact: string
  acknowledge: boolean
}
export const newReserveDraft = (poolId = ""): ReserveDraft => ({
  poolId,
  owner: "docker_network",
  resource: "",
  exact: "",
  acknowledge: false,
})

const OWNERS: { value: IPAMOwner; product?: string; glyph: typeof Layers }[] = [
  { value: "docker_network", product: "docker", glyph: Layers },
  { value: "wireguard_server", product: "wireguard", glyph: Layers },
  { value: "interface_address", glyph: Bridge },
  { value: "namespace_address", glyph: Layers },
  { value: "provider_range", glyph: Globe },
]

export function ReserveSheet({
  open,
  onOpenChange,
  pools,
  draft,
  onDraft,
  gaps,
  stale,
  busy,
  error,
  onReserve,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The pools still handing out blocks. */
  pools: IPAMPool[]
  draft: ReserveDraft
  onDraft: (draft: ReserveDraft) => void
  /** Sources the planner could not read: reserving needs them acknowledged. */
  gaps: number
  /** The plan on screen is the last known one; nothing is reserved against it. */
  stale: boolean
  busy: boolean
  error?: string
  onReserve: () => void
}) {
  const pool = pools.find((row) => row.id === draft.poolId)
  const ready = Boolean(
    pool && draft.resource.trim() && (gaps === 0 || draft.acknowledge) && !stale,
  )
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Reserve a block"
      description="Hold a block of a pool for a named owner."
      width="md"
      footer={
        <>
          {stale && (
            <FormNote tone="warning" className="mr-auto">
              The plan could not be refreshed.
            </FormNote>
          )}
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="ipam-reserve" pending={busy} disabled={!ready || busy}>
            Reserve
          </Button>
        </>
      }
    >
      <form
        id="ipam-reserve"
        className="space-y-6"
        onSubmit={(event) => {
          event.preventDefault()
          if (ready && !busy) onReserve()
        }}
      >
        <Field label="Shared pool" htmlFor="ipam-reserve-pool">
          <Select
            value={draft.poolId}
            onValueChange={(next) => {
              if (pools.some((row) => row.id === next)) onDraft({ ...draft, poolId: next })
            }}
            disabled={busy}
          >
            <SelectTrigger id="ipam-reserve-pool" className="w-full">
              <SelectValue placeholder="Select…" />
            </SelectTrigger>
            <SelectContent>
              {pools.map((row) => (
                <SelectItem key={row.id} value={row.id}>
                  {row.name} · {row.prefix}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <div className="space-y-2">
          <p id="ipam-reserve-owner" className="text-body font-medium">
            Intended owner
          </p>
          <ChoiceGrid columns={2} role="group" aria-labelledby="ipam-reserve-owner">
            {OWNERS.map((owner, index) => (
              <ChoiceCard
                key={owner.value}
                index={index}
                selected={draft.owner === owner.value}
                disabled={busy}
                onClick={() => onDraft({ ...draft, owner: owner.value })}
                className="min-h-0 flex-row items-center gap-3"
              >
                <ProductLogo id={owner.product} fallback={owner.glyph} size="sm" />
                <ChoiceCardTitle>{ownerLabel[owner.value]}</ChoiceCardTitle>
              </ChoiceCard>
            ))}
          </ChoiceGrid>
          {draft.owner !== "docker_network" && draft.owner !== "wireguard_server" && (
            <FormNote>
              This owner receives an advisory planning prefix. Its native form remains responsible
              for validating and applying configuration.
            </FormNote>
          )}
        </div>
        <FieldRow>
          <Field
            label="Exact native resource name"
            htmlFor="ipam-reserve-resource"
            hint="The creation form must keep this exact owner and name."
          >
            <Input
              id="ipam-reserve-resource"
              value={draft.resource}
              onChange={(event) => onDraft({ ...draft, resource: event.target.value })}
              className="font-mono"
              disabled={busy}
              spellCheck={false}
            />
          </Field>
          <Field
            label="Exact prefix (optional)"
            htmlFor="ipam-reserve-exact"
            hint={
              pool
                ? `Leave empty for the first free /${pool.allocationBits}.`
                : "Select a pool first."
            }
          >
            <Input
              id="ipam-reserve-exact"
              value={draft.exact}
              onChange={(event) => onDraft({ ...draft, exact: event.target.value })}
              className="font-mono"
              disabled={busy}
              spellCheck={false}
            />
          </Field>
        </FieldRow>
        {gaps > 0 && (
          <OptionRow
            title="I have reviewed the incomplete coverage"
            hint="This reserves planning space and does not prove absence of foreign allocations."
            checked={draft.acknowledge}
            onCheckedChange={(acknowledge) => onDraft({ ...draft, acknowledge })}
            disabled={busy}
          />
        )}
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </form>
    </SidePanel>
  )
}

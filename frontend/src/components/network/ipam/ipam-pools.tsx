"use client"

import { Bridge, Eye, Globe, Hash, Layers, Plus, Trash, type Icon } from "@/components/icons"
import { BarReading } from "@/components/bar-reading"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogos } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu } from "@/components/verbs"
import { plural } from "@/lib/format"
import type { IPAMPool, IPAMView } from "@/lib/network-ipam"
import { exact, ownerProduct, poolBand, poolOwners } from "@/components/network/ipam/reading"

/** The planner's own hue, and the one for what a native owner was seen holding. */
const PLANNED = "var(--chart-1)"
const OBSERVED = "var(--chart-3)"

const OWNER_GLYPH: Record<string, Icon> = {
  interface: Bridge,
  namespace: Layers,
  provider: Globe,
}
const OWNER_WORD: Record<string, string> = {
  docker: "Docker",
  wireguard: "WireGuard",
  tailscale: "Tailscale",
  interface: "an interface",
  namespace: "a namespace",
  provider: "the provider",
  route: "a route",
}

/**
 * The pools, a card each: one band of the blocks the planner holds, the
 * blocks native owners were seen holding, and what is left, with who holds
 * space in it as logos. A table of six exact counts stood here, and the
 * reader turned them into "how full is it" by hand; the counts are still the
 * exact ones the server returned, on the band's line and in its head.
 */
export function IPAMPools({
  view,
  loading,
  canCreate,
  canRetire,
  busy,
  children,
  onCreate,
  onRetire,
}: {
  view?: IPAMView
  loading: boolean
  canCreate: boolean
  canRetire: boolean
  busy: boolean
  /** The prefix preview, between the title and the bands. */
  children?: React.ReactNode
  onCreate: () => void
  onRetire: (pool: IPAMPool) => void
}) {
  const pools = view?.pools ?? []
  const active = pools.filter((pool) => !pool.retiredAt).length
  return (
    <Panel plain aria-label="Pools">
      <PanelHeader
        title="Pools"
        actions={
          <span className="flex items-center gap-3">
            <InfoTip label="How the blocks are counted">
              Counts are exact. A block both planned and observed is counted once as unavailable.
              Free blocks are free within readable evidence; coverage gaps can hide additional
              allocations.
            </InfoTip>
            {pools.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {active}
                {pools.length > active && ` · ${pools.length - active} retired`}
              </span>
            )}
            {canCreate && (
              <Button size="sm" variant="outline" onClick={onCreate}>
                <Plus className="size-3.5" />
                New pool
              </Button>
            )}
          </span>
        }
      />
      <PanelBody className="space-y-5">
        {children}
        {loading ? (
          <div aria-hidden className="grid gap-4 lg:grid-cols-2">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-[154px] rounded-xl" />
            ))}
          </div>
        ) : pools.length === 0 ? (
          <EmptyState
            mark={<ProductLogos ids={["docker", "wireguard", "tailscale"]} />}
            title="No pools yet"
            description="A pool is a network set aside for planning. Blocks are reserved from it for Docker networks, WireGuard tunnels and the rest."
            action={
              canCreate && (
                <Button size="sm" onClick={onCreate}>
                  <Plus className="size-3.5" />
                  New pool
                </Button>
              )
            }
          />
        ) : (
          <ul aria-label="Address pools" className="grid gap-4 lg:grid-cols-2">
            {pools.map((pool) => (
              <li key={pool.id} className="min-w-0 rounded-xl border px-4 pt-3 pb-2">
                <PoolBand
                  pool={pool}
                  view={view!}
                  canRetire={canRetire}
                  busy={busy}
                  onRetire={onRetire}
                />
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

function PoolBand({
  pool,
  view,
  canRetire,
  busy,
  onRetire,
}: {
  pool: IPAMPool
  view: IPAMView
  canRetire: boolean
  busy: boolean
  onRetire: (pool: IPAMPool) => void
}) {
  const use = view.utilization.find((item) => item.poolId === pool.id)
  const band = use ? poolBand(use) : undefined
  const owners = poolOwners(pool, view)
  const blocks = (value: string) =>
    `${exact(value)} ${BigInt(value) === BigInt(1) ? "block" : "blocks"}`
  // The spans need shares, which a float holds; the figures stay the exact strings.
  const SCALE = 1_000_000
  return (
    <BarReading
      label={`${pool.name}, ${pool.prefix}`}
      list={`What holds ${pool.name}`}
      fixed
      capacity={SCALE}
      free="Free"
      format={() => (use ? blocks(use.candidateBlocks) : "Unknown")}
      title={
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate">{pool.name}</span>
          <span className="truncate font-mono text-hint font-normal text-muted-foreground">
            {pool.prefix} → /{pool.allocationBits}
          </span>
        </span>
      }
      figure={
        <span className="flex h-7 items-center gap-2 text-hint text-muted-foreground">
          {pool.retiredAt ? (
            <span>Retired</span>
          ) : (
            use && (
              <span className="numeric max-sm:hidden">
                <span className="font-medium text-foreground">{exact(use.candidateBlocks)}</span> of{" "}
                {exact(use.totalBlocks)} free
              </span>
            )
          )}
          {canRetire && !pool.retiredAt && (
            <VerbMenu
              label={`Actions for ${pool.name}`}
              verbs={[
                {
                  key: "retire",
                  label: "Retire",
                  icon: Trash,
                  danger: true,
                  disabled: busy,
                  run: () => onRetire(pool),
                },
              ]}
            />
          )}
        </span>
      }
      message={
        !use || !band ? "Blocks unknown. No utilization was returned for this pool." : undefined
      }
      parts={
        use && band
          ? [
              {
                key: "planned",
                value: band.share(band.planned) * SCALE,
                color: PLANNED,
                mark: <Hash className="size-3.5" style={{ color: PLANNED }} />,
                name: "Planned",
                detail: "held by reservations",
                figure: blocks(use.reservedBlocks),
                label: `planned ${blocks(use.reservedBlocks)}`,
              },
              {
                key: "observed",
                value: band.share(band.observedOnly) * SCALE,
                color: OBSERVED,
                mark: <Eye className="size-3.5" style={{ color: OBSERVED }} />,
                name: "Observed",
                detail:
                  use.coverage === "observed" ? "seen on native owners" : "seen; coverage unknown",
                figure: blocks(use.observedBlocks),
                label: `observed ${blocks(use.observedBlocks)}`,
                title: `${blocks(use.unavailableBlocks)} unavailable once planned and observed are joined`,
              },
            ]
          : []
      }
      note={
        use && (
          <>
            <span title="Planned and observed, a block both hold counted once">
              {exact(use.unavailableBlocks)} unavailable
            </span>
            {owners.length > 0 && <Owners owners={owners} />}
          </>
        )
      }
    />
  )
}

/** Who holds space in a pool, as their own marks. */
function Owners({ owners }: { owners: string[] }) {
  const words = owners.map((owner) => OWNER_WORD[owner] ?? owner.replaceAll("_", " "))
  return (
    <span
      role="img"
      aria-label={`Held by ${words.join(", ")}`}
      title={`Held by ${words.join(", ")}`}
      className="flex shrink-0 items-center gap-1.5"
    >
      {owners.map((owner) => {
        const product = ownerProduct(owner)
        const Glyph = OWNER_GLYPH[owner] ?? Hash
        return product ? (
          <ProductGlyph key={owner} id={product} />
        ) : (
          <Glyph key={owner} aria-hidden className="size-3.5" />
        )
      })}
    </span>
  )
}

/** "2 pools" for the identity line, counting only the ones still handing out blocks. */
export const poolsWord = (count: number) => plural(count, "pool")

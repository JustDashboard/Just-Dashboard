"use client"

import { Globe, NetworkDevice, SecureConnection, Shield, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import type { FirewallStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo, portProduct } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { firewallProduct, limitedKeys } from "@/components/network/firewall-words"
import {
  deniedSources,
  openings,
  refusesByDefault,
  type Opening,
} from "@/components/network/firewall-reading"

/** More openings than this and the rest are one line before the default. */
const MOST = 5

type MarkSize = "lg" | "md" | "sm"

/** What the firewall's log adds up to over its window, for the picture's nodes. */
export type FirewallCounts = {
  /** Packets no rule let in. */
  blocked?: number
  /** The distinct addresses they came from. */
  sources?: number
  /** Connections a rate limit turned away. */
  limited?: number
  /** The window in a few words: "in 24h". */
  window: string
  /** Narrow the log under the picture to what a figure counts. */
  onBlocked: () => void
  onLimited: () => void
}

/**
 * What the firewall does with a connection arriving from outside, drawn as
 * the path it takes: in from the internet, through the firewall, and out to
 * each place a rule admits it — one mark per port, drawn as the product that
 * usually answers there — or into the default, which is where everything no
 * rule matched goes.
 *
 * The rule list under it is the same facts in the firewall's own order, which
 * is the order it reads them in and the one a change has to be made in; this
 * is the same rules folded by where they lead (`firewall-reading.ts`), which
 * is how the question "what can reach this machine?" is asked.
 *
 * The line is the state, as on every wiring picture (`WireFlow`): moving
 * where traffic is admitted while the firewall enforces, red into a port the
 * backend says must never be open to everyone, amber where the default lets
 * the rest in or the firewall is off and nothing it says is being done, and
 * dashed into a default that turns everything away.
 *
 * What the log says the rules did stood in a row of tiles under the picture.
 * It is said where it happened now: what was refused, and from how many
 * addresses, under the default that refused it; what a rate limit turned
 * away under the port it limits. Each is a press that narrows the log at the
 * foot of the page to those lines. With two limited ports, or none, the log
 * cannot say which port a refusal was at, so the figure joins the default's.
 */
export function FirewallPicture({
  status,
  counts,
}: {
  status: FirewallStatus
  /** Absent while the firewall is not logging: a zero would mean "not recorded". */
  counts?: FirewallCounts
}) {
  const all = openings(status)
  const shown = all.slice(0, MOST)
  const denied = deniedSources(status)
  const enforcing = status.enabled
  const policy = status.policy?.incoming
  const fenced = refusesByDefault(policy)
  const limited = limitedKeys(status)
  // The one port a limit's refusals can be laid at.
  const limitedAt = limited.size === 1 ? [...limited][0] : undefined
  const product = firewallProduct(status.backend)

  const admitted = (opening: Opening, index: number): WireLine => ({
    state: opening.danger ? "danger" : enforcing ? "up" : "warning",
    moving: enforcing && !opening.danger,
    duration: 2.4,
    delay: 0.6 + index * 0.25,
  })

  const end: WireFlowNode[] = shown.map((opening, index) => {
    const counted = counts && opening.key === limitedAt
    return {
      id: `opening:${opening.key}`,
      mark: (size) => <OpeningMark opening={opening} size={size} />,
      title: <OpeningName opening={opening} />,
      name: opening.service ?? opening.port ?? opening.key,
      hint: (
        <>
          <OpeningReach opening={opening} limited={limited.has(opening.key)} />
          {counted && (
            <span className="numeric block text-warning">
              {counts.limited === undefined
                ? "reading the log…"
                : `${counts.limited.toLocaleString()} turned away ${counts.window}`}
            </span>
          )}
        </>
      ),
      ...(counted
        ? {
            onPress: counts.onLimited,
            label: `Show the lines a limit turned away at ${opening.port ?? opening.key}`,
          }
        : {}),
      line: admitted(opening, index),
    }
  })
  if (all.length > MOST)
    end.push({
      id: "openings:more",
      mark: (size) => (
        <WireMark tone="logo" shape="square" size={size}>
          <span className="numeric text-hint">+{all.length - MOST}</span>
        </WireMark>
      ),
      title: (
        <span className="font-normal text-muted-foreground">{all.length - MOST} more open</span>
      ),
      name: `${all.length - MOST} more open`,
      href: "#rules",
      line: { state: enforcing ? "up" : "warning" },
    })
  // Laid at the default when no single port owns the limit's refusals.
  const strays = counts && !limitedAt && (counts.limited ?? 0) > 0 ? counts.limited : undefined
  end.push({
    id: "default",
    mark: (size) => (
      <WireMark tone={fenced ? "neutral" : "warning"} shape="square" size={size}>
        <Shield aria-hidden />
      </WireMark>
    ),
    title: "Everything else",
    name: "Everything else",
    hint: (
      <>
        <span className={cn("block", !fenced && "text-warning")}>
          {policy ? `${policy} by default` : "no default reported"}
        </span>
        {counts && (
          <span className="numeric block" data-testid="refused-reading">
            {counts.blocked === undefined
              ? "reading the log…"
              : `${counts.blocked.toLocaleString()} refused${
                  counts.sources === undefined
                    ? ""
                    : ` from ${counts.sources.toLocaleString()} address${counts.sources === 1 ? "" : "es"}`
                }${strays ? ` · ${strays.toLocaleString()} rate-limited` : ""} ${counts.window}`}
          </span>
        )}
      </>
    ),
    ...(counts ? { onPress: counts.onBlocked, label: "Show the lines the firewall refused" } : {}),
    line: {
      state: enforcing && fenced ? "idle" : "warning",
    },
  })

  return (
    <div data-slot="firewall-picture">
      <WireFlow
        className="animate-rise"
        hubShare={0.7}
        start={{
          title: "Arrives",
          nodes: [
            {
              id: "internet",
              mark: (size) => (
                <WireMark tone="logo" shape="square" size={size}>
                  <Globe aria-hidden />
                </WireMark>
              ),
              eyebrow: "Inbound",
              title: "The internet",
              name: "The internet",
              hint:
                denied.length > 0 ? (
                  <span className="text-destructive">
                    {denied.length} address{denied.length === 1 ? "" : "es"} denied by name
                  </span>
                ) : (
                  "every address"
                ),
              line: {
                state: enforcing ? "up" : "warning",
                moving: enforcing,
                duration: 2.4,
              },
            },
          ],
        }}
        end={{ title: "Answered by", nodes: end }}
        hub={{
          mark: (size) => (
            <WireMark tone={enforcing ? "logo" : "danger"} shape="square" size={size}>
              {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Shield aria-hidden />}
            </WireMark>
          ),
          eyebrow: "Firewall",
          title: status.backend,
          hint: enforcing ? (
            "first matching rule wins"
          ) : (
            <span className="text-destructive">inactive — nothing enforced</span>
          ),
        }}
      />
    </div>
  )
}

/** The picture before the firewall has answered: its hub and grey rows where the ports will be. */
export function FirewallPictureLoading() {
  return (
    <div data-slot="firewall-picture" data-loading>
      <WireFlow
        loading
        hubShare={0.7}
        start={{ title: "Arrives", nodes: [], loadingRows: 1 }}
        end={{ title: "Answered by", nodes: [], loadingRows: 4 }}
        hub={{
          mark: (size) => (
            <WireMark tone="logo" shape="square" size={size}>
              <Shield aria-hidden />
            </WireMark>
          ),
          eyebrow: "Firewall",
        }}
      />
    </div>
  )
}

/** The product that usually answers on the port, else the kind of thing a port is. */
function OpeningMark({ opening, size }: { opening: Opening; size: MarkSize }) {
  const product = portProduct(Number(opening.port))
  const Glyph: Icon = opening.port === "22" ? SecureConnection : NetworkDevice
  return (
    <WireMark tone={opening.danger ? "danger" : "logo"} shape="square" size={size}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />}
    </WireMark>
  )
}

function OpeningName({ opening }: { opening: Opening }) {
  return (
    <span className="flex min-w-0 items-baseline gap-2">
      {opening.service && <span className="truncate">{opening.service}</span>}
      {opening.port ? (
        <span className="numeric shrink-0 font-mono font-normal">
          <span className="text-muted-foreground">:</span>
          <span className="text-[var(--tag-pink)]">{opening.port}</span>
          {opening.protocol && <span className="text-muted-foreground">/{opening.protocol}</span>}
        </span>
      ) : (
        !opening.service && <span className="truncate font-mono">{opening.key}</span>
      )}
    </span>
  )
}

/** Who may reach it: everyone, or the networks the rules name, each as where it is. */
function OpeningReach({ opening, limited }: { opening: Opening; limited: boolean }) {
  if (opening.danger) return <span className="line-clamp-2 text-destructive">{opening.danger}</span>
  if (opening.anyone)
    return <span className="block">from anyone{limited && " · rate-limited"}</span>
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
      from
      {opening.sources.slice(0, 2).map((source) => {
        const network = networkOf(source.split("/")[0])
        return (
          <span
            key={source}
            className="inline-flex min-w-0 items-center gap-1"
            title={network.label}
          >
            {network.product && <ProductGlyph id={network.product} />}
            <span className="truncate font-mono text-foreground">{source}</span>
          </span>
        )
      })}
      {opening.sources.length > 2 && <span>+{opening.sources.length - 2}</span>}
      {limited && <span>· rate-limited</span>}
    </span>
  )
}

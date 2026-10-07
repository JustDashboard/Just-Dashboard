"use client"

import { useRef, type RefObject } from "react"
import { Globe, NetworkDevice, SecureConnection, Shield, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import type { FirewallStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo, portProduct } from "@/components/product-logo"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import {
  deniedSources,
  openings,
  refusesByDefault,
  type Opening,
} from "@/components/network/firewall-reading"

/** More openings than this and the rest are one line under the last. */
const MOST = 5

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
 * The line is the state, as on every wiring picture: moving where traffic is
 * admitted while the firewall enforces, red into a port the backend says must
 * never be open to everyone, amber where the default lets the rest in or the
 * firewall is off and nothing it says is being done, and dashed into a
 * default that turns everything away.
 */
export function FirewallPicture({ status }: { status: FirewallStatus }) {
  const container = useRef<HTMLDivElement>(null)
  const inbound = useRef<HTMLDivElement>(null)
  const wall = useRef<HTMLDivElement>(null)
  const rest = useRef<HTMLDivElement>(null)

  const all = openings(status)
  const shown = all.slice(0, MOST)
  const denied = deniedSources(status)
  const enforcing = status.enabled
  const policy = status.policy?.incoming
  const fenced = refusesByDefault(policy)

  return (
    <SettingPicture
      label="How a connection from outside is answered"
      containerRef={container}
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={inbound}
            toRef={wall}
            still={!enforcing}
            tone={enforcing ? "default" : "warning"}
            duration={2.4}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={wall}
            toRef={rest}
            shape="s"
            still
            dashed={enforcing && fenced}
            tone={enforcing && fenced ? "default" : "warning"}
          />
        </>
      }
      start={[
        <WireNode
          key="inbound"
          nodeRef={inbound}
          align="end"
          mark={
            <WireMark tone="logo" shape="square">
              <Globe aria-hidden />
            </WireMark>
          }
          eyebrow="Inbound"
          title="The internet"
          hint={
            denied.length > 0 ? (
              <span className="text-destructive">
                {denied.length} address{denied.length === 1 ? "" : "es"} denied by name
              </span>
            ) : (
              "every address"
            )
          }
        />,
      ]}
      middle={
        <WireNode
          nodeRef={wall}
          align="center"
          mark={
            <WireMark tone={enforcing ? "logo" : "danger"} shape="square">
              <Shield aria-hidden />
            </WireMark>
          }
          eyebrow="Firewall"
          title={status.backend}
          hint={
            enforcing ? (
              "first matching rule wins"
            ) : (
              <span className="text-destructive">inactive — nothing enforced</span>
            )
          }
        />
      }
      end={
        <ul className="flex min-w-0 flex-col gap-4">
          {shown.map((opening, index) => (
            <OpeningNode
              key={opening.key}
              opening={opening}
              container={container}
              wall={wall}
              enforcing={enforcing}
              index={index}
            />
          ))}
          <li className="min-w-0">
            <WireNode
              nodeRef={rest}
              mark={
                <WireMark tone={fenced ? "neutral" : "warning"} shape="square" size="md">
                  <Shield aria-hidden />
                </WireMark>
              }
              title="Everything else"
              hint={
                <span className={cn(!fenced && "text-warning")}>
                  {all.length > MOST && `${all.length - MOST} more open · `}
                  {policy ? `${policy} by default` : "no default reported"}
                </span>
              }
            />
          </li>
        </ul>
      }
    />
  )
}

/**
 * One place the rules admit traffic to, with the line into it from the
 * firewall. The line is drawn here rather than with the others so each
 * opening owns the ref its line ends on; the line is positioned against the
 * picture's container like every other, not against this item.
 */
function OpeningNode({
  opening,
  container,
  wall,
  enforcing,
  index,
}: {
  opening: Opening
  container: RefObject<HTMLDivElement | null>
  wall: RefObject<HTMLDivElement | null>
  enforcing: boolean
  index: number
}) {
  const node = useRef<HTMLDivElement>(null)
  return (
    <li className="min-w-0">
      <AnimatedBeam
        containerRef={container}
        fromRef={wall}
        toRef={node}
        shape="s"
        still={!enforcing || Boolean(opening.danger)}
        tone={opening.danger ? "danger" : enforcing ? "default" : "warning"}
        duration={2.4}
        delay={0.6 + index * 0.25}
      />
      <WireNode
        nodeRef={node}
        mark={<OpeningMark opening={opening} />}
        title={<OpeningName opening={opening} />}
        hint={<OpeningReach opening={opening} />}
      />
    </li>
  )
}

/** The product that usually answers on the port, else the kind of thing a port is. */
function OpeningMark({ opening }: { opening: Opening }) {
  const product = portProduct(Number(opening.port))
  const Glyph: Icon = opening.port === "22" ? SecureConnection : NetworkDevice
  return (
    <WireMark tone={opening.danger ? "danger" : "logo"} shape="square" size="md">
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
function OpeningReach({ opening }: { opening: Opening }) {
  if (opening.danger) return <span className="line-clamp-2 text-destructive">{opening.danger}</span>
  if (opening.anyone) return <>from anyone</>
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
    </span>
  )
}

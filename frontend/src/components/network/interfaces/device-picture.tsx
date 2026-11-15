"use client"

import { useMemo } from "react"
import { Globe, Layers, LockClosed, RotateClockwise, Router, Servers } from "@/components/icons"
import { rate } from "@/lib/format"
import type { NetworkLink } from "@/lib/types"
import { WireMark } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo, imageProduct } from "@/components/product-logo"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { RX, TX } from "@/components/network/rate-pair"
import {
  LinkGlyph,
  ROLE_LABEL,
  carrying,
  kindLabel,
  linkProduct,
  pulseDuration,
} from "@/components/network/marks"
import {
  deviceHref,
  devicePicture,
  deviceState,
  type DeviceTab,
  type PictureNode,
} from "./device-facts"

/** A column draws this many ends and folds the rest into one "more". */
const DRAWN = 5

/**
 * What a device is wired to, on the shared picture: on the left the far side
 * of its wire (the internet behind an uplink, a tunnel's other end, the
 * namespace a veth leads into, the card a VLAN rides on, a bridge's ports),
 * the device in the middle with its in and out, and on the right what this
 * server does with it (the bridge it is a port of, what rides on it, where
 * the server answers). Every end that is a thing with a page opens it. A wire
 * moves while the device carries more than a kilobyte a second, towards the
 * side most of the bytes go, and is dashed and still while it is down.
 */
export function DevicePicture({
  link,
  links,
  onOpenNamespace,
  onTab,
}: {
  link: NetworkLink
  links: NetworkLink[]
  onOpenNamespace: (kind: "named" | "container", name: string) => void
  onTab: (tab: DeviceTab) => void
}) {
  const state = deviceState(link)
  const picture = useMemo(() => devicePicture(link, links), [link, links])
  const byName = useMemo(() => new Map(links.map((l) => [l.name, l])), [links])
  const moving = state.up && carrying(link)
  const duration = pulseDuration(link.rxRate + link.txRate)

  const node = (described: PictureNode, side: "away" | "here"): WireFlowNode => {
    const other = described.device ? byName.get(described.device) : undefined
    const otherUp = other ? deviceState(other).up : true
    const line: WireLine =
      !state.up || !otherUp
        ? { state: "down" }
        : described.kind === "address"
          ? { state: "up", moving: false }
          : {
              state: "up",
              moving,
              duration,
              // Received bytes come from the far side of the wire and are
              // handed on to this server's side.
              reverse: side === "away" ? link.rxRate < link.txRate : link.rxRate >= link.txRate,
            }
    const inside = described.namespace
    return {
      id: described.id,
      mark: <NodeMark node={described} device={other} links={links} />,
      eyebrow: described.eyebrow,
      title: described.mono ? (
        <span className="font-mono">{described.title}</span>
      ) : (
        described.title
      ),
      name: described.title,
      hint: described.hint,
      href: described.device ? deviceHref(described.device) : described.href,
      onPress: inside
        ? () => onOpenNamespace(inside.kind, inside.name)
        : described.tab
          ? () => onTab(described.tab!)
          : undefined,
      label: inside
        ? inside.kind === "named"
          ? `Open namespace ${inside.name}`
          : `Open ${inside.name}’s namespace`
        : described.device
          ? `Open ${described.device}`
          : described.tab
            ? `${link.name}’s addresses`
            : undefined,
      line,
    }
  }

  return (
    <WireFlow
      className="animate-rise"
      start={{
        title: "Leads to",
        nodes: picture.away.map((n) => node(n, "away")),
        max: DRAWN,
        more: { onPress: () => onTab("ports"), words: (hidden) => `${hidden} more ports` },
        empty:
          link.kind === "dummy" ? "No wire: it only holds addresses" : "Nothing on its far side",
      }}
      end={{
        title: "On this server",
        nodes: picture.here.map((n) => node(n, "here")),
        max: DRAWN,
        empty: "No address, and nothing rides on it",
      }}
      hub={{
        mark: (
          <WireMark tone="logo" shape="square" size="lg">
            <DeviceGlyph link={link} />
          </WireMark>
        ),
        eyebrow:
          kindLabel(link).toLowerCase() === ROLE_LABEL[link.role].toLowerCase()
            ? ROLE_LABEL[link.role]
            : `${ROLE_LABEL[link.role]} · ${kindLabel(link)}`,
        title: <span className="block truncate font-mono">{link.name}</span>,
        hint: state.up ? (
          <span className="numeric font-mono">
            <span style={{ color: RX }}>↓ {rate(link.rxRate)}</span>{" "}
            <span style={{ color: TX }}>↑ {rate(link.txRate)}</span>
          </span>
        ) : (
          state.label.toLowerCase()
        ),
      }}
    />
  )
}

function DeviceGlyph({ link }: { link: Pick<NetworkLink, "kind" | "owner" | "containerImage"> }) {
  const product = linkProduct(link)
  return product && hasProductLogo(product) ? (
    <ProductGlyph id={product} />
  ) : (
    <LinkGlyph kind={link.kind} />
  )
}

function NodeMark({
  node,
  device,
  links,
}: {
  node: PictureNode
  device?: NetworkLink
  links: NetworkLink[]
}) {
  const glyph = () => {
    if (device) return <DeviceGlyph link={device} />
    switch (node.kind) {
      case "internet":
        return <Globe aria-hidden />
      case "self":
        return <RotateClockwise aria-hidden />
      case "peers":
        return <ProductGlyph id={node.eyebrow === "Tailscale" ? "tailscale" : "wireguard"} />
      case "remote":
        return <Router aria-hidden />
      case "address":
        return <Servers aria-hidden />
      case "namespace": {
        // A container's namespace is drawn as what the container runs.
        const image = links.find(
          (l) => l.container === node.namespace?.name && l.containerImage,
        )?.containerImage
        const product = image ? imageProduct(image) : undefined
        return product && hasProductLogo(product) ? (
          <ProductGlyph id={product} />
        ) : (
          <Layers aria-hidden />
        )
      }
      case "device":
        return <LinkGlyph kind="veth" />
      default:
        return <LockClosed aria-hidden />
    }
  }
  return (
    <WireMark tone="logo" shape="square" size="md">
      {glyph()}
    </WireMark>
  )
}

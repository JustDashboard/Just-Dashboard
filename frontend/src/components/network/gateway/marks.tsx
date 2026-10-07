"use client"

import { Globe, Servers } from "@/components/icons"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { LinkGlyph, LinkMark, linkProduct } from "@/components/network/marks"
import { targetProduct } from "@/components/network/gateway/reading"

/**
 * A port the way every Network page draws one: the number in the port hue
 * after a colon that stays quiet, so `:8080` is read as a port first and as
 * punctuation second.
 */
export function Port({ port, className }: { port: string; className?: string }) {
  return (
    <span className={cn("font-mono", className)}>
      <span className="text-muted-foreground">:</span>
      <span className="text-[var(--tag-pink)]">{port}</span>
    </span>
  )
}

/** A target's address with its port in the port hue; an IPv6 address keeps its brackets. */
export function Endpoint({
  address,
  port,
  className,
}: {
  address: string
  port: string
  className?: string
}) {
  const v6 = address.includes(":")
  return (
    <span className={cn("font-mono", className)}>
      {v6 ? `[${address}]` : address}
      <Port port={port} />
    </span>
  )
}

/**
 * Where a forward arrives, as a mark in a wiring picture: the internet for
 * "any device", otherwise the device named — the product that made it where
 * one did, its kind's glyph where not.
 */
export function ArrivalMark({ device, links }: { device: string; links: NetworkLink[] }) {
  const link = links.find((l) => l.name === device)
  const product = link ? linkProduct(link) : undefined
  return (
    <WireMark tone="logo" shape="square" size="md">
      {!device ? (
        <Globe aria-hidden />
      ) : product ? (
        <ProductGlyph id={product} />
      ) : (
        <LinkGlyph kind={link?.kind ?? "physical"} />
      )}
    </WireMark>
  )
}

/** A forward's target as the product that usually answers on its port, or a server where none does. */
export function TargetMark({ ports, targetPort }: { ports: string; targetPort: string }) {
  const product = targetProduct({ ports, targetPort })
  return (
    <WireMark tone="logo" shape="square" size="md">
      {product ? <ProductGlyph id={product} /> : <Servers aria-hidden />}
    </WireMark>
  )
}

/** The same two marks on the smaller tile a list row carries. */
export function TargetLogo({ ports, targetPort }: { ports: string; targetPort: string }) {
  return <ProductLogo id={targetProduct({ ports, targetPort })} size="sm" fallback={Servers} />
}

/** A device on the tile a list row carries: what made it, else its kind. */
export function DeviceLogo({ device, links }: { device: string; links: NetworkLink[] }) {
  const link = links.find((l) => l.name === device)
  if (!link) return <ProductLogo size="sm" fallback={Globe} />
  return <LinkMark link={link} />
}

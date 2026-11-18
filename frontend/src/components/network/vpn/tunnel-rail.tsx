"use client"

import { useSearchParams } from "next/navigation"
import { plural } from "@/lib/format"
import { ProductLogo } from "@/components/product-logo"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { tunnelHref } from "./tunnel-reading"

export type RailTunnel = { name: string; line: string; tone: DotTone; archived: boolean }

/**
 * The tunnels, beside the one that is open: the live ones with who is online,
 * then the removed ones whose record is still kept, so the next tunnel is one
 * press away and Alt+↑ and Alt+↓ walk the list. The VPN page's list has no
 * filter, so neither has this. Each row is a link to the tunnel on the view on
 * screen; a removed tunnel's words are in the muted ink.
 */
export function TunnelRail({
  tunnels,
  current,
  loading,
  error,
  onRetry,
  onOpen,
  className,
}: {
  tunnels: RailTunnel[]
  current: string
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  /** Opens a tunnel carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="tunnel-rail"
      label="Tunnels"
      caption={
        loading && tunnels.length === 0 ? "Reading the tunnels…" : plural(tunnels.length, "tunnel")
      }
      rows={tunnels}
      keyOf={(tunnel) => tunnel.name}
      current={(tunnel) => tunnel.name === current}
      row={(tunnel, open) => (
        <RailRow
          item={tunnel.name}
          name={tunnel.name}
          current={open}
          href={tunnelHref(tunnel.name, tab)}
          onOpen={onOpen && (() => onOpen(tunnel.name))}
          quiet={tunnel.archived}
          mark={
            <ProductLogo id="wireguard" size="sm" className="size-7 rounded-md [&_img]:size-4" />
          }
          dot={<StatusDot tone={tunnel.tone} className="size-1.5" />}
        >
          <RailName current={open} mono>
            {tunnel.name}
          </RailName>
          <RailLine className="numeric">{tunnel.line}</RailLine>
        </RailRow>
      )}
      loading={loading}
      loadingRows={3}
      error={error}
      onRetry={onRetry}
      className={className}
    />
  )
}

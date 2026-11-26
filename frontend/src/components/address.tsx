"use client"

import { cn } from "@/lib/utils"

/*
 * Addresses as the product writes them, for every area that draws one: Docker's
 * networks, the gateway, protection, routing and the interfaces. It lived in
 * components/network until 0.9.0; `components/network/address.tsx` re-exports
 * it for the callers that have not moved.
 */

/**
 * An address in CIDR form with its prefix in the muted ink, as the
 * Configuration page's allowlist draws a network: the address is read first,
 * the size of the network second, and a mask is no reading to colour. A host route's prefix — /32 for
 * IPv4, /128 for IPv6 — is left off, since an address alone already means one address. An IPv6 /32 is a
 * network, and keeps its prefix.
 */
export function Cidr({ cidr, className }: { cidr: string; className?: string }) {
  const [address, bits] = cidr.split("/")
  const host = bits !== undefined && bits === (address.includes(":") ? "128" : "32")
  return (
    <span className={cn("font-mono", className)}>
      {address}
      {bits !== undefined && !host && (
        <>
          <span className="text-muted-foreground">/{bits}</span>
        </>
      )}
    </span>
  )
}

/** A device's addresses on one line, global ones first, link-local left out unless it is all there is. */
export function AddressLine({
  addresses,
  max = 2,
  className,
}: {
  addresses: { cidr: string; scope: string }[]
  max?: number
  className?: string
}) {
  const global = addresses.filter((a) => a.scope !== "link" && a.scope !== "host")
  const shown = (global.length > 0 ? global : addresses).slice(0, max)
  if (shown.length === 0)
    return <span className={cn("text-muted-foreground", className)}>no address</span>
  const more = (global.length > 0 ? global.length : addresses.length) - shown.length
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-x-2 truncate", className)}>
      {shown.map((a) => (
        <Cidr key={a.cidr} cidr={a.cidr} />
      ))}
      {more > 0 && <span className="text-muted-foreground">+{more}</span>}
    </span>
  )
}

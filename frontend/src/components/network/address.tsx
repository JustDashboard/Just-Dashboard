"use client"

import { cn } from "@/lib/utils"

/**
 * An address in CIDR form with its prefix in the port hue, as the
 * Configuration page's allowlist draws a network: the address is read first,
 * the size of the network second. A host route's /32 or /128 is left off,
 * since an address alone already means one address.
 */
export function Cidr({ cidr, className }: { cidr: string; className?: string }) {
  const [address, bits] = cidr.split("/")
  const host = bits !== undefined && ((address.includes(":") && bits === "128") || bits === "32")
  return (
    <span className={cn("font-mono", className)}>
      {address}
      {bits !== undefined && !host && (
        <>
          <span className="text-muted-foreground">/</span>
          <span className="text-[var(--tag-pink)]">{bits}</span>
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

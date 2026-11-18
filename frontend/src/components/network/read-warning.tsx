"use client"

import { StaleNotice } from "@/components/stale"

/**
 * Retained state is useful during a failed poll, provided it is not presented as a fresh read.
 *
 * @deprecated use `StaleNotice` from `@/components/stale`, which this is with Network's default
 * `reading`.
 */
export function NetworkReadWarning({
  reading = "network state",
  ...props
}: Omit<React.ComponentProps<typeof StaleNotice>, "reading"> & { reading?: string }) {
  return <StaleNotice reading={reading} {...props} />
}

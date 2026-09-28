"use client"

import { LockClosed } from "@/components/icons"
import { InputGroupToggle } from "@/components/ui/input-group"

/**
 * Whether the name is served over HTTPS, as the last segment of the field it
 * belongs to. It is `InputGroupToggle` with the lock and the words fixed, so
 * `/deploy/new`'s address and the Domains rows name the scheme the same way
 * and a reader who learnt it on one finds it on the other.
 */
export function HttpsToggle({
  checked,
  onChange,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <InputGroupToggle
      icon={LockClosed}
      label="HTTPS"
      aria-label="Serve this hostname over HTTPS"
      pressed={checked}
      onPressedChange={onChange}
    />
  )
}

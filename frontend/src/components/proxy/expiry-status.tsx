import type { Certificate } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Meter } from "@/components/meter"
import { Status } from "@/components/status-dot"
import type { Tone } from "@/components/tone"

/** A certificate's expiry as a verdict — a dot and a word, not a filled pill. */
export function ExpiryStatus({ cert }: { cert?: Certificate }) {
  if (!cert) return <Status state="created" label="unchecked" />
  if (cert.error) return <Status verdict="critical" label={cert.error.slice(0, 40)} />
  if (cert.expired) return <Status verdict="critical" label="expired" />
  if (cert.expiring) return <Status verdict="warning" label={`${cert.daysLeft}d left`} />
  return <Status verdict="ok" label={`${cert.daysLeft}d left`} />
}

/** The tone a certificate's figures take: red once refused, amber inside the renewal window. */
export function expiryTone(cert: Pick<Certificate, "expired" | "expiring" | "error">): Tone {
  if (cert.error || cert.expired) return "danger"
  if (cert.expiring) return "warning"
  return "default"
}

/**
 * How much of its term a certificate has left, as the meter the readings
 * draw under a figure (§7): full when just issued, empty at expiry. The term
 * is the certificate's own — ninety days for Let's Encrypt, a year for most
 * others — so a run of these reads as "how far along", not as days against
 * an arbitrary bar. Beside the verdict, because "29d left" means one thing
 * on a ninety-day certificate and another on a two-year one.
 */
export function CertLife({
  cert,
  className,
}: {
  cert: Pick<Certificate, "notBefore" | "notAfter" | "daysLeft" | "expired" | "expiring" | "error">
  className?: string
}) {
  const term = Math.max(
    1,
    (new Date(cert.notAfter).getTime() - new Date(cert.notBefore).getTime()) / 86_400_000,
  )
  const left = cert.error || cert.expired ? 0 : Math.max(0, Math.min(1, cert.daysLeft / term))
  return (
    <Meter
      value={left * 100}
      tone={expiryTone(cert)}
      size="thin"
      label={`${cert.daysLeft} of ${Math.round(term)} days left`}
      className={cn("w-20", className)}
    />
  )
}

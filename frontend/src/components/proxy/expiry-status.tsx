import { certificateTone, certificateWords, type CertificateFacts } from "@/lib/certificates"
import type { Certificate } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Meter } from "@/components/meter"
import { Status, type Verdict } from "@/components/status-dot"
import type { Tone } from "@/components/tone"

/**
 * A certificate's expiry as a verdict — a dot and a word, not a filled pill.
 * The word and the verdict are lib/certificates' `certificateWords` (short)
 * and `certificateTone`, which every certificate reading shares: a test
 * certificate says so before anything about its days, and the last day is
 * counted in hours ("10h left"), not "0d left".
 */
export function ExpiryStatus({ cert, now }: { cert?: CertificateFacts; now?: number }) {
  if (!cert) return <Status state="created" label="Unchecked" />
  return (
    <Status
      verdict={certificateTone(cert)}
      label={certificateWords(cert, { form: "short", now })}
    />
  )
}

/** The tone a certificate's figures take: red once refused, amber inside the renewal window. */
export function expiryTone(
  cert: Pick<Certificate, "expired" | "expiring" | "error" | "staging">,
): Tone {
  return TONE_OF[certificateTone({ ...cert, daysLeft: 0 })]
}

const TONE_OF: Record<Verdict, Tone> = {
  ok: "default",
  notice: "default",
  warning: "warning",
  critical: "danger",
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
  cert: Pick<
    Certificate,
    "notBefore" | "notAfter" | "daysLeft" | "expired" | "expiring" | "error" | "staging"
  >
  className?: string
}) {
  // A certificate that could not be read has no dates, so no term to be part
  // way through.
  const days = (Date.parse(cert.notAfter ?? "") - Date.parse(cert.notBefore ?? "")) / 86_400_000
  const term = Number.isFinite(days) ? Math.max(1, days) : 1
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

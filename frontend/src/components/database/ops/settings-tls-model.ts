import type { DbTLS } from "@/lib/types"

/**
 * What the TLS section says and asks, worked out from what the server reported.
 * No React.
 */

/** A change the section can make. */
export type TLSChange = "on" | "allow" | "require" | "off"

const DAY = 24 * 60 * 60 * 1000

/** Whole days from now to an instant; negative once it has passed. */
export function daysUntil(iso: string | undefined, now: number): number | undefined {
  if (!iso) return undefined
  const at = new Date(iso).getTime()
  return Number.isNaN(at) ? undefined : Math.floor((at - now) / DAY)
}

/** The state in two words and the sentence under the section's picture. */
export function tlsState(tls: Pick<DbTLS, "supported" | "enabled" | "require" | "canOptional">): {
  word: string
  on: boolean
  sentence: string
} {
  if (!tls.enabled && !tls.supported) {
    return {
      word: "Not managed here",
      on: false,
      sentence: "The dashboard does not hold this server's certificate.",
    }
  }
  if (!tls.enabled) {
    return {
      word: "Off",
      on: false,
      sentence:
        "The server speaks plain text. Anything between the client and the server can read the password and the data.",
    }
  }
  if (!tls.canOptional) {
    return {
      word: "TLS only",
      on: true,
      sentence:
        "The port speaks TLS and nothing else: a client that does not use TLS cannot connect.",
    }
  }
  if (tls.require) {
    return {
      word: "TLS required",
      on: true,
      sentence: "A client that does not use TLS is refused.",
    }
  }
  return {
    word: "TLS allowed",
    on: true,
    sentence: "A client may use TLS or plain text. Switch to required to refuse plain text.",
  }
}

/** The certificate's lifetime as a sentence, with the day the renewal takes it up. */
export function certificateLife(
  tls: Pick<DbTLS, "notAfter" | "renewAt" | "renewedAt">,
  now: number,
): string {
  const left = daysUntil(tls.notAfter, now)
  if (left === undefined) return "—"
  if (left < 0) return "Expired"
  if (left === 0) return "Ends today"
  const renewIn = daysUntil(tls.renewAt, now)
  const base = `${left} ${left === 1 ? "day" : "days"} left`
  return renewIn !== undefined && renewIn > 0 ? base : `${base}, renews on its own`
}

/**
 * What a change does, for the dialog that asks. Every one of these recreates
 * the container; the dialog says so, and what the people who connect should
 * expect.
 */
export function changeEffects(
  change: TLSChange,
  tls: Pick<DbTLS, "linked" | "canOptional" | "container">,
  engine: string,
): { title: string; confirm: string; lead: string; cautions: string[] } {
  const container = tls.container ?? "the container"
  const restart = `Recreates ${container} with the certificate mounted. The data is kept. Every open session is dropped while the container is replaced.`
  const linked =
    tls.linked > 0
      ? [
          `${tls.linked} deployment ${tls.linked === 1 ? "environment reaches" : "environments reach"} this database by its internal name. Once TLS is required, each has to connect with TLS.`,
        ]
      : []
  switch (change) {
    case "on":
      return {
        title: `Turn on TLS for ${engine}`,
        confirm: "Turn on",
        lead: restart,
        cautions: tls.canOptional
          ? [
              "Plain text is still accepted until you require TLS. This dashboard's own connection starts verifying the certificate now.",
            ]
          : [
              "This engine's port speaks one protocol, so plain text stops being accepted. Anything that connects without TLS has to be changed.",
              ...linked,
            ],
      }
    case "require":
      return {
        title: `Require TLS for ${engine}`,
        confirm: "Require",
        lead: restart,
        cautions: ["A client that does not use TLS is refused from now on.", ...linked],
      }
    case "allow":
      return {
        title: `Allow plain text for ${engine}`,
        confirm: "Allow",
        lead: restart,
        cautions: ["Clients may connect without TLS again."],
      }
    case "off":
      return {
        title: `Turn off TLS for ${engine}`,
        confirm: "Turn off",
        lead: `Recreates ${container} the way it was before, and removes the certificate. The data is kept. Every open session is dropped while the container is replaced.`,
        cautions: ["Anything that connects with TLS has to stop using it."],
      }
  }
}

/** The audit-free summary toast after a change. */
export function changedWords(change: TLSChange, name: string): string {
  switch (change) {
    case "on":
      return `${name} now speaks TLS`
    case "require":
      return `${name} refuses connections without TLS`
    case "allow":
      return `${name} accepts plain text again`
    case "off":
      return `${name} no longer speaks TLS`
  }
}

/** The names a certificate carries, addresses first as the server sends them. */
export function namesLine(names: string[]): string {
  return names.join(", ")
}

/** The first characters of an authority's fingerprint, grouped to be read aloud. */
export function shortFingerprint(hex: string): string {
  return (
    hex
      .slice(0, 16)
      .toUpperCase()
      .match(/.{1,4}/g)
      ?.join(" ") ?? ""
  )
}

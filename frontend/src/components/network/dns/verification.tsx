"use client"

import type { DNSVerification } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { CHECK_KIND_NAME, checkTone } from "@/components/network/dns/resolvers"

/**
 * A resolver change's checks, as one list: the plan before the change (every
 * check planned, required ones marked) and the result after it (each with
 * what it found). The scopes no check reaches follow, because one name
 * resolving does not mean every scope the change touched still answers.
 */
export function VerificationChecks({
  verification,
  label,
}: {
  verification: DNSVerification
  label: string
}) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <ol aria-label={label} className="flex min-w-0 flex-col divide-y divide-hairline">
        {verification.checks.map((check, index) => (
          <li
            key={`${check.kind}:${check.name ?? ""}:${index}`}
            className="flex min-w-0 flex-col gap-1 py-2 first:pt-0"
          >
            <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <p className="min-w-0 text-body font-medium break-words">
                {CHECK_KIND_NAME[check.kind]}
                {check.name && (
                  <>
                    {" "}
                    <span className="font-mono">{check.name}</span>
                  </>
                )}
                <span className="font-normal text-muted-foreground"> · {check.scope}</span>
              </p>
              <Status
                tone={checkTone(check.state)}
                label={
                  check.state === "planned"
                    ? check.required
                      ? "Required"
                      : "Reported"
                    : check.state.charAt(0).toUpperCase() + check.state.slice(1)
                }
              />
            </div>
            {(check.answer || check.millis) && (
              <p className="numeric text-hint text-muted-foreground">
                {check.answer && (
                  <>
                    {check.type} <span className="font-mono">{check.answer}</span>
                  </>
                )}
                {check.millis ? ` in ${Math.round(check.millis)} ms` : ""}
              </p>
            )}
            {check.detail && (
              <p className="text-hint break-words text-muted-foreground">{check.detail}</p>
            )}
          </li>
        ))}
      </ol>
      {verification.unverified.length > 0 && (
        <div className="flex min-w-0 flex-col gap-1">
          <p className="text-body font-medium">Not checked</p>
          <ul aria-label="Not checked" className="flex flex-col gap-1">
            {verification.unverified.map((scope) => (
              <li key={scope} className="text-hint break-words text-muted-foreground">
                {scope}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

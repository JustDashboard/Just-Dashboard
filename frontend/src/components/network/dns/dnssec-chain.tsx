"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { post } from "@/lib/api"
import type { DNSSECChain, DNSSECLevel } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { Question } from "@/components/network/dns/lookup-question"

const VERDICT: Record<
  DNSSECChain["verdict"],
  { label: string; tone: "running" | "notice" | "warning" | "danger" }
> = {
  secure: { label: "Secure to the root", tone: "running" },
  anchored: { label: "Secure to a local anchor", tone: "running" },
  insecure: { label: "Insecure", tone: "warning" },
  broken: { label: "Broken", tone: "danger" },
  unknown: { label: "Unknown", tone: "notice" },
}

const LINK: Record<DNSSECLevel["link"], string> = {
  digest_match: "DS digest recomputed and matched",
  digest_mismatch: "DS matches no key",
  no_ds: "No DS from the parent",
  root_anchor: "Matches an IANA root anchor",
  root_anchor_mismatch: "Matches no IANA root anchor",
  not_checked: "",
}

const SET: Record<DNSSECLevel["dnskey"]["state"], string> = {
  authenticated: "authenticated",
  unauthenticated: "not authenticated",
  absent: "none",
  not_found: "no such name",
  validation_failed: "rejected by validation",
  error: "unreadable",
  not_asked: "not asked",
}

/**
 * The chain of trust one name rests on, from its zone to the root. Each zone's
 * DNSKEY and DS sets come from the identified systemd-resolved with its own
 * authentication flag; every DS digest is then recomputed here from the
 * child's keys, and the root's keys compared with the IANA anchors. Parents
 * outside the name's own policy scope are not asked, so a private name's
 * chain ends at its scope's edge rather than at a public resolver.
 */
export function DNSSECChainCheck({
  name,
  onName,
}: {
  name: string
  onName: (name: string) => void
}) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [chain, setChain] = useState<DNSSECChain>()
  if (!can("system.admin")) return null
  const walk = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setChain(await post<DNSSECChain>("/network/dns/dnssec-chain", { name: name.trim() }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <Question
        id="dnssec-chain-name"
        name={name}
        onName={onName}
        verb="Trace"
        ready={Boolean(name.trim())}
        busy={busy}
        error={error}
        info="Asks the native resolver for each zone's DNSKEY and DS sets inside the name's own policy scope."
        onSubmit={() => void walk()}
      />
      {chain && (
        <section
          aria-label={`Chain of trust for ${chain.name}`}
          className="flex min-w-0 flex-col gap-4"
        >
          <div className="flex flex-wrap items-baseline justify-between gap-3 border-b border-hairline pb-2">
            <p className="min-w-0 text-body font-medium">
              <span className="font-mono">{chain.name}</span>
              <span className="numeric font-normal text-muted-foreground">
                {" "}
                · {chain.questions} native questions
              </span>
            </p>
            <Status tone={VERDICT[chain.verdict].tone} label={VERDICT[chain.verdict].label} />
          </div>
          <p className="text-body text-muted-foreground">{chain.summary}</p>
          <ol className="flex min-w-0 flex-col divide-y divide-hairline">
            {chain.levels.map((level) => (
              <li key={level.zone} className="flex min-w-0 flex-col gap-1 py-3">
                <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3">
                  <p className="min-w-0 text-body font-medium">
                    <span className="font-mono">{level.zone}</span>
                    {level.scope && (
                      <span className="font-normal text-muted-foreground"> · {level.scope}</span>
                    )}
                  </p>
                  {level.link !== "not_checked" && (
                    <Status
                      tone={
                        level.link === "digest_match" || level.link === "root_anchor"
                          ? "running"
                          : level.link === "no_ds"
                            ? "notice"
                            : "danger"
                      }
                      label={LINK[level.link]}
                    />
                  )}
                </div>
                {level.role === "apex" || level.role === "unavailable" ? (
                  <p className="numeric text-hint text-muted-foreground">
                    DNSKEY {SET[level.dnskey.state]} ({level.dnskey.records}) · DS{" "}
                    {SET[level.ds.state]}
                    {level.ds.records > 0 && ` (${level.ds.records})`}
                    {level.keys.length > 0 &&
                      ` · keys ${level.keys.map((k) => `${k.keyTag}/${k.algorithm}${k.sep ? " KSK" : ""}`).join(", ")}`}
                  </p>
                ) : null}
                <p className="text-hint break-words text-muted-foreground">{level.detail}</p>
              </li>
            ))}
          </ol>
          {chain.limitations.map((line) => (
            <p key={line} className="text-hint text-muted-foreground">
              {line}
            </p>
          ))}
        </section>
      )}
    </div>
  )
}

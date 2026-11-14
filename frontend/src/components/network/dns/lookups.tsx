"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { Segments } from "@/components/deploy/settings/segments"
import { DNSSECChainCheck } from "@/components/network/dns/dnssec-chain"
import { LookupRace } from "@/components/network/dns/lookup-race"
import { DNSPolicyEvidence } from "@/components/network/dns/policy-evidence"
import { lookupToolOf, type LookupTool } from "@/components/network/dns/views"

const TOOL_NAME: Record<LookupTool, string> = {
  resolve: "Resolve",
  policy: "Policy",
  dnssec: "DNSSEC chain",
}

/**
 * The page's three lookups as one small workbench: resolve a name (and race
 * chosen resolvers on it), investigate the policy a private name is resolved
 * under, and trace the chain of trust a name rests on. They were three
 * sections at the foot of the page, each under a paragraph, each asking for
 * the name again.
 *
 * One name is held for all three, the lookup is a segmented choice over the
 * question (`?tool=` in the address), and each lookup keeps what it last
 * answered while another is in front, so comparing a resolution with its chain
 * is two presses and no retyping. An account is offered only the lookups its
 * role may run: the policy investigation and the chain are an administrator's.
 */
export function Lookups({
  tool,
  onTool,
}: {
  /** The `?tool=` of the address. */
  tool: string | null
  onTool: (tool: LookupTool) => void
}) {
  const { can } = useAuth()
  const [name, setName] = useState("")
  const [type, setType] = useState("A")
  const tools: LookupTool[] = can("system.admin") ? ["resolve", "policy", "dnssec"] : ["resolve"]
  const shown = lookupToolOf(tool, tools)
  return (
    <div className="flex min-w-0 flex-col gap-6">
      {tools.length > 1 && (
        <Segments
          label="Lookup"
          value={shown}
          options={tools.map((value) => ({ value, label: TOOL_NAME[value] }))}
          onChange={onTool}
          className="self-start"
        />
      )}
      {/* Hidden rather than unmounted: a lookup keeps its answer while another is in front. */}
      <div hidden={shown !== "resolve"}>
        <LookupRace name={name} type={type} onName={setName} onType={setType} />
      </div>
      {tools.includes("policy") && (
        <div hidden={shown !== "policy"}>
          <DNSPolicyEvidence
            name={name}
            type={type}
            onName={setName}
            onType={setType}
            active={shown === "policy"}
          />
        </div>
      )}
      {tools.includes("dnssec") && (
        <div hidden={shown !== "dnssec"}>
          <DNSSECChainCheck name={name} onName={setName} />
        </div>
      )}
    </div>
  )
}

"use client"

import { useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Page } from "@/components/page"
import { FirewallPanel } from "@/components/network/firewall-panel"
import { handoffFromParams } from "@/components/security/rule-form"
import { useSecurity } from "@/components/security/security-context"

export default function NetworkFirewallPage() {
  const {
    firewall,
    firewallLoading,
    firewallError,
    refreshFirewall,
    refreshPosture,
    posture,
    applyFix,
  } = useSecurity()
  // Read once: the link is where the reader arrived, and the dialog it opens
  // is theirs from then on. Taken out of the address bar so a reload does
  // not open the same rule again.
  const params = useSearchParams()
  const [handoff] = useState(() => handoffFromParams(params))
  useEffect(() => {
    if (!handoff) return
    window.history.replaceState(null, "", window.location.pathname)
  }, [handoff])

  return (
    <Page className="animate-rise">
      <FirewallPanel
        status={firewall}
        posture={posture}
        loading={firewallLoading}
        error={firewallError}
        onFix={applyFix}
        handoff={handoff}
        refresh={() => {
          refreshFirewall()
          refreshPosture()
        }}
      />
    </Page>
  )
}

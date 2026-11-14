"use client"

import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { ReachabilitySection } from "@/components/database/ops/settings-reach"
import { TransportSecuritySection } from "@/components/database/ops/settings-tls"
import { useDatabase } from "@/components/database/shell/database-context"

export function SettingsNetwork() {
  const { engine } = useDatabase()
  return (
    <SettingsFrame page="network">
      {() => (
        <>
          <ReachabilitySection />
          {engine.can("serverTLS") && <TransportSecuritySection />}
        </>
      )}
    </SettingsFrame>
  )
}

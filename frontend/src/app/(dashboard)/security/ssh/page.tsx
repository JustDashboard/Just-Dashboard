"use client"

import { Page } from "@/components/page"
import { SSHPanel } from "@/components/security/ssh-panel"
import { useSecurity } from "@/components/security/security-context"

export default function SecuritySSHPage() {
  const { posture, applyFix } = useSecurity()

  return (
    <Page className="animate-rise">
      <SSHPanel posture={posture} onFix={applyFix} />
    </Page>
  )
}

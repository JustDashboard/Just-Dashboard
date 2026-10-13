"use client"

import { Page } from "@/components/page"
import { SSHPanel } from "@/components/security/ssh-panel"
import { useSecurity } from "@/components/security/security-context"

export default function SecuritySSHPage() {
  const {} = useSecurity()

  return (
    <Page className="animate-rise">
      <SSHPanel />
    </Page>
  )
}

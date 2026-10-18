"use client"

import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { ApiKeysView, useApiKeys } from "@/components/account/api-keys"
import { useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * Keys, read as the credentials they are: where each one reaches, drawn as
 * the product that holds it wired through the role it carries into this
 * server's API, then the keys under chips that count and narrow them, then
 * the command that uses one.
 *
 * It opened on four tiles — in use, used this week, never used, expiring
 * soon — and the operator asked for them to go. In use is the list's count;
 * the other three are chips that narrow the list to the keys they count, and
 * wires in the picture: a key called today pulses, one never called is
 * dashed, one about to stop is amber. An administrator reads everyone's keys,
 * so the page asks for the accounts too, to put a face on each key that is
 * not theirs.
 */
export default function AccountKeysPage() {
  const { can } = useAuth()
  const keys = useApiKeys()
  const users = useDashboardUsers(can("system.admin"))
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="API keys" />
      <ApiKeysView keys={keys} users={users.data ?? undefined} />
    </Page>
  )
}

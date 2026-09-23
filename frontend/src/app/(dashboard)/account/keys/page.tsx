"use client"

import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader } from "@/components/page"
import { ApiKeysView, CreateApiKeyDialog, useApiKeys } from "@/components/account/api-keys"
import { useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * Keys, read as the credentials they are: four readings an operator asks of
 * any list of them — how many open a door, which are used, which never were,
 * which are about to stop — then the keys drawn as what holds them, and the
 * command that uses one.
 *
 * It opened on a paragraph saying why anybody would want a key, over a framed
 * table of prefixes and roles. The paragraph is now the empty state, which is
 * the one time it is news; the command it carried is the page's last block.
 * An administrator reads everyone's keys, so the page asks for the accounts
 * too, to put a face on each key that is not theirs.
 */
export default function AccountKeysPage() {
  const { can } = useAuth()
  const keys = useApiKeys()
  const users = useDashboardUsers(can("system.admin"))
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="API keys"
        actions={keys.data?.length ? <CreateApiKeyDialog onDone={keys.refresh} /> : undefined}
      />
      <ApiKeysView keys={keys} users={users.data ?? undefined} />
    </Page>
  )
}

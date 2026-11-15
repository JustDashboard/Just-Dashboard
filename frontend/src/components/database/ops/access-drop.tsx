"use client"

import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { accountKey } from "@/components/database/ops/access-model"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import type { DbRole } from "@/components/database/ops/access-types"

/**
 * The question asked before an account is dropped, with the account named.
 * The row's menu and the account's own page both ask it.
 */
export function dropAccountRequest(
  id: number,
  role: Pick<DbRole, "name" | "host" | "connections">,
  onDropped: () => void,
): ConfirmRequest {
  const label = accountKey(role)
  return {
    title: "Drop account",
    confirmLabel: "Drop",
    subject: {
      mark: <AccountMark name={role.name} />,
      name: <AccountName name={role.name} host={role.host} className="text-body" />,
      facts: (
        <FormFact label="Sessions now">{role.connections > 0 ? role.connections : "none"}</FormFact>
      ),
    },
    description: (
      <>
        <p>
          Removes the account from the server. Whatever signs in with it stops signing in, and what
          it was granted goes with it.
        </p>
        <p>
          The engine refuses, and changes nothing, while the account still owns something in a
          database.
        </p>
      </>
    ),
    action: async () => {
      await del(`/databases/${id}/server/roles/${encodeURIComponent(role.name)}`, {
        query: role.host !== undefined ? { host: role.host } : undefined,
      })
      notify.success(`Dropped ${label}`)
      onDropped()
      return "reported"
    },
  }
}

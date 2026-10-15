"use client"

import { Page, PageContext } from "@/components/page"
import { DashboardUsersView, useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The people who can sign in to this dashboard. The page opens on them as one
 * identity line — their faces, who can sign in, who administers, who was here
 * last — with the verdict at its end: how many accounts a password alone
 * opens. Under it are the accounts as cards, each drawn by its face with its
 * role, what guards it and when it last signed in, opening its editor, under
 * chips that count and narrow them.
 *
 * It opened on four tiles — accounts, administrators, two-factor, signed in
 * this week — and the operator asked for them to go. Accounts is the line's
 * title and the All chip; administrators a fact and a chip; two-factor the
 * verdict and the Password only chip; the sign-ins each card's last reading.
 * A band of three blocks — last sign-ins on an axis, two-factor and roles as
 * bars — replaced them for a while, and went too, at the operator's request:
 * every figure in it was already on the cards under it.
 */
export default function AccountUsersPage() {
  const users = useDashboardUsers()
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Users" />
      <DashboardUsersView users={users} />
    </Page>
  )
}

"use client"

import { Page, PageContext } from "@/components/page"
import { DashboardUsersView, useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The people who can sign in to this dashboard. The page opens on them as one
 * identity line — their faces, who can sign in, who administers, who was here
 * last — with the verdict at its end: how many accounts a password alone
 * opens. Under it a band says who signed in when, what guards each account
 * and what each may do, each as the people themselves in their own hues; then
 * the accounts as cards, each drawn by its face and opening its editor, under
 * chips that count and narrow them.
 *
 * It opened on four tiles — accounts, administrators, two-factor, signed in
 * this week — and the operator asked for them to go. Accounts is the line's
 * title and the All chip; administrators a fact, a chip and the Roles block;
 * two-factor the verdict, the Password only chip and the Two-factor block;
 * this week's sign-ins the Last sign-ins block.
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

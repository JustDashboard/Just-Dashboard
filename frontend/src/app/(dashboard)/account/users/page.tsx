"use client"

import { Page, PageHeader } from "@/components/page"
import {
  CreateDashboardUserDialog,
  DashboardUsersView,
  useDashboardUsers,
} from "@/components/account/dashboard-users"

/**
 * The people who can sign in to this dashboard. Four readings — how many,
 * who holds everything, how many a password alone would open, who has been
 * here this week — over the accounts as cards, each drawn by its face and
 * opening its editor. It was a framed table of names and switches.
 */
export default function AccountUsersPage() {
  const users = useDashboardUsers()
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="Users"
        actions={<CreateDashboardUserDialog onDone={users.refresh} />}
      />
      <DashboardUsersView users={users} />
    </Page>
  )
}

"use client"

import { Page, PageHeader } from "@/components/page"
import {
  CreateDashboardUserDialog,
  DashboardUsersTable,
  useDashboardUsers,
} from "@/components/account/dashboard-users"

export default function AccountUsersPage() {
  const users = useDashboardUsers()
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="Users"
        actions={<CreateDashboardUserDialog onDone={users.refresh} />}
      />
      <DashboardUsersTable users={users} />
    </Page>
  )
}

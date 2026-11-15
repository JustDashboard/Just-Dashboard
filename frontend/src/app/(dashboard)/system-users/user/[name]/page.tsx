"use client"

import { Suspense } from "react"
import { AccountPage } from "@/components/system-users/account-page"
import { AdminOnly } from "@/components/system-users/read-state"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function SystemUserPage() {
  return (
    <AdminOnly title="System account">
      <Suspense>
        <AccountPage />
      </Suspense>
    </AdminOnly>
  )
}

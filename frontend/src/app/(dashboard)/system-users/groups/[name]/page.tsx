"use client"

import { Suspense } from "react"
import { GroupPage } from "@/components/system-users/group-page"
import { AdminOnly } from "@/components/system-users/read-state"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function SystemGroupRoute() {
  return (
    <AdminOnly title="Group">
      <Suspense>
        <GroupPage />
      </Suspense>
    </AdminOnly>
  )
}

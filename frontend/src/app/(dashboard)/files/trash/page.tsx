"use client"

import { Suspense } from "react"
import { TrashView } from "@/components/files/trash-view"

// Reading register: the Trash is the Files workbench's frame with what was
// deleted in place of a folder's listing.
export default function TrashPage() {
  return (
    <Suspense>
      <TrashView />
    </Suspense>
  )
}

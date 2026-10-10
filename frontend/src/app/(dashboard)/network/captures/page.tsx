"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { Captures } from "@/components/network/captures"

export default function PacketCapturesPage() {
  return (
    <Page className="animate-rise">
      <Suspense>
        <Captures />
      </Suspense>
    </Page>
  )
}

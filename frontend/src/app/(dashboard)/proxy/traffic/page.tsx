"use client"

import { Suspense } from "react"
import { SiteTrafficView } from "@/components/proxy/site-traffic-view"

export default function ProxyTrafficPage() {
  return (
    <Suspense>
      <SiteTrafficView />
    </Suspense>
  )
}

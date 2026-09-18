"use client"

import { Suspense } from "react"
import { SitesPage } from "@/components/proxy/vhosts-panel"
import { useProxy } from "@/components/proxy/proxy-context"

export default function ProxySitesPage() {
  const { hasNginx } = useProxy()
  return (
    <Suspense>
      <SitesPage hasNginx={hasNginx} />
    </Suspense>
  )
}

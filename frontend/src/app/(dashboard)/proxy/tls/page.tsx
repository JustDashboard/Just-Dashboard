"use client"

import { Suspense } from "react"
import { TLSPage } from "@/components/proxy/tls-fleet"

export default function ProxyTLSPage() {
  return (
    <Suspense>
      <TLSPage />
    </Suspense>
  )
}

"use client"

import { Suspense } from "react"
import { TLSReportPage } from "@/components/proxy/tls-report"

export default function ProxyTLSPage() {
  return (
    <Suspense>
      <TLSReportPage />
    </Suspense>
  )
}

"use client"

import { Suspense } from "react"
import { CertificatesPage } from "@/components/proxy/certs-panel"

export default function ProxyCertificatesPage() {
  return (
    <Suspense>
      <CertificatesPage />
    </Suspense>
  )
}

"use client"

import { Suspense } from "react"
import { CertificateWorkProvider } from "@/components/proxy/certificate-work"

/**
 * The certificates and each certificate's page share certbot's state, the job
 * console and the forms that issue and import (`certificate-work.tsx`), so a
 * run started on one is still on screen on the other.
 */
export default function CertificatesLayout({ children }: { children: React.ReactNode }) {
  return (
    <Suspense>
      <CertificateWorkProvider>{children}</CertificateWorkProvider>
    </Suspense>
  )
}

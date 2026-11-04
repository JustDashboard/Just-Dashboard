"use client"

import { Suspense } from "react"
import { DatabaseAssistant } from "@/components/database/assistant"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AssistantPage() {
  return (
    // It reads `?chat=` from the address.
    <Suspense>
      <SectionPage section="assistant" any={DatabaseAssistant} />
    </Suspense>
  )
}

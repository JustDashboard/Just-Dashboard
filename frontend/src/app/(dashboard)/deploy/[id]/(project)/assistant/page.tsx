"use client"

import { Suspense } from "react"
import { ProjectAssistant } from "@/components/deploy/project-assistant"

export default function Page() {
  return (
    <Suspense>
      <ProjectAssistant />
    </Suspense>
  )
}

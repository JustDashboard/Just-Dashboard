"use client"

import { Generate } from "@/components/database/generate"
import { SectionPage } from "@/components/database/shell/section-page"

export default function GeneratePage() {
  return <SectionPage section="generate" sql={Generate} />
}

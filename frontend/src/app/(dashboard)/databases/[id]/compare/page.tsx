"use client"

import { Compare } from "@/components/database/compare"
import { SectionPage } from "@/components/database/shell/section-page"

export default function ComparePage() {
  return <SectionPage section="compare" sql={Compare} />
}

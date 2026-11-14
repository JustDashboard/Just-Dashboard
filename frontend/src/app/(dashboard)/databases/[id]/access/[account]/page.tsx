"use client"

import { AccountPage } from "@/components/database/ops/account-page"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AccountRoute() {
  return <SectionPage section="access" any={AccountPage} />
}

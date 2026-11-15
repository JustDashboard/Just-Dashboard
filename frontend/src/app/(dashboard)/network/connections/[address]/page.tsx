"use client"

import { Suspense } from "react"
import { AddressPage } from "@/components/network/connections/address-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function NetworkAddressRoute() {
  return (
    <Suspense>
      <AddressPage />
    </Suspense>
  )
}

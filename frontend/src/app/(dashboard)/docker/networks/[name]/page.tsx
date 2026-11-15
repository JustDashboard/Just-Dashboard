import { Suspense } from "react"
import { NetworkPage } from "@/components/docker/network-page"

// The open view lives in the query string, which the App Router only hands
// out inside a Suspense boundary.
export default function DockerNetworkPage() {
  return (
    <Suspense>
      <NetworkPage />
    </Suspense>
  )
}

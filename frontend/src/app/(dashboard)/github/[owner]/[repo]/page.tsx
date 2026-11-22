"use client"

import { Suspense } from "react"
import { GitHubRepoPage } from "@/components/github/repo-page"

// The view on screen and the account it is read through live in the query
// string, which the App Router only hands out inside a Suspense boundary.
export default function GitHubRepoRoute() {
  return (
    <Suspense>
      <GitHubRepoPage />
    </Suspense>
  )
}

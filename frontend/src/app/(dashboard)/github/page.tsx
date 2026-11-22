import { Suspense } from "react"
import { GitHubListPage, GitHubListSkeleton } from "@/components/github/list-page"

export default function GitHubPage() {
  return (
    // The list page's own loading shape, so reading the address never draws
    // a second layout first.
    <Suspense fallback={<GitHubListSkeleton />}>
      <GitHubListPage />
    </Suspense>
  )
}

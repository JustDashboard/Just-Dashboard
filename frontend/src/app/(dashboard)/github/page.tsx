import { Suspense } from "react"
import { Page, PageContext } from "@/components/page"
import { IdentityHeader } from "@/components/identity-header"
import { ChoiceListSkeleton } from "@/components/choice-row"
import { GitHubListPage } from "@/components/github/list-page"

export default function GitHubPage() {
  return (
    <Suspense
      fallback={
        // The list page's own loading shape, so the address bar's read
        // never draws a second layout first.
        <Page>
          <PageContext title="GitHub" />
          <IdentityHeader
            label="About GitHub"
            slot="github-identity"
            loading
            mark="github"
            title="GitHub"
            command
            menu={false}
          />
          <ChoiceListSkeleton rows={6} lines={2} />
        </Page>
      }
    >
      <GitHubListPage />
    </Suspense>
  )
}

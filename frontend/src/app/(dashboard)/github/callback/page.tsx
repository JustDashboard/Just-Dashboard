"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { GitHubMark } from "@/components/icons"
import { notify } from "@/lib/toast"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { Spinner } from "@/components/loading"
import { Button } from "@/components/ui/button"
import { deniedWords, takeGitHubReturn } from "@/components/github/callback"

/**
 * Where GitHub sends the browser back after creating the repository App or
 * signing a person in. The redirect is a cross-site navigation, so the code
 * is exchanged here under the page's own session, once (`takeGitHubReturn`),
 * and the page then replaces itself with the GitHub page on the account it
 * connected. It holds one shape while it works: the mark, one line and a
 * spinner, in the box the failure would take.
 */
export default function GitHubCallbackPage() {
  const router = useRouter()
  const [failure, setFailure] = useState<
    { kind: "error"; error: Error } | { kind: "denied"; text: string } | undefined
  >()

  useEffect(() => {
    const { found, result } = takeGitHubReturn()
    // Said after the effect, as the exchange's answer is: the page draws its
    // waiting shape first either way.
    const denied =
      found.kind === "denied"
        ? deniedWords(found.error, found.description)
        : "GitHub sent nothing back to finish. Start the sign-in again from the GitHub page."
    let alive = true
    if (!result) {
      void Promise.resolve().then(() => alive && setFailure({ kind: "denied", text: denied }))
      return () => {
        alive = false
      }
    }
    result.then(
      (done) => {
        if (!alive) return
        if (done.kind === "app") {
          notify.success("The GitHub App is set up", {
            description: "Install it on an account and connect GitHub to list its repositories.",
          })
          router.replace("/github")
        } else {
          if (done.connection) notify.success(`Connected @${done.connection.login}`)
          router.replace(done.connection ? `/github?account=${done.connection.id}` : "/github")
        }
      },
      (error: Error) => alive && setFailure({ kind: "error", error }),
    )
    return () => {
      alive = false
    }
  }, [router])

  const back = (
    <Button size="sm" variant="outline" asChild>
      <Link href="/github">Back to GitHub</Link>
    </Button>
  )

  return (
    <Page>
      <PageContext title="Connecting GitHub" />
      {failure?.kind === "error" ? (
        <div className="flex flex-col items-start gap-3">
          <ErrorState error={failure.error} className="w-full" />
          {back}
        </div>
      ) : failure?.kind === "denied" ? (
        <EmptyState
          mark={<GitHubMark aria-hidden className="size-5 text-muted-foreground" />}
          title="Not connected"
          description={failure.text}
          action={back}
        />
      ) : (
        <EmptyState
          aria-busy
          mark={<Spinner className="size-5 text-muted-foreground" />}
          title="Finishing with GitHub"
          description="This takes a moment."
        />
      )}
    </Page>
  )
}

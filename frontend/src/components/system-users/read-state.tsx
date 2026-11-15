"use client"

import Link from "next/link"
import { LockClosed } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { useAuth } from "@/hooks/use-auth"
import { ApiError } from "@/lib/api"

/**
 * The host's accounts are an administrator's to read: the list alone shows
 * shells, groups and how many keys open each account, so the server refuses
 * every route of this section to anyone else, reads included. A page drawn for
 * somebody without the role would be a form of controls that all fail, so it is
 * this instead: what the section is, why it is closed, and the way out.
 */
function Closed() {
  return (
    <EmptyState
      icon={LockClosed}
      title="The host's accounts are an administrator's to read"
      description="This section shows who can sign in to the server and how far each reaches, so it is kept to dashboard accounts with the administrator role."
      action={
        <Link href="/" className="text-body font-medium text-brand hover:underline">
          Back to the overview
        </Link>
      }
    />
  )
}

/** Draws the page for an administrator, and the closed door for anyone else. */
export function AdminOnly({ title, children }: { title: string; children: React.ReactNode }) {
  const { can, loading } = useAuth()
  if (loading || can("system.admin")) return <>{children}</>
  return (
    <Page className="animate-rise">
      <PageContext title={title} />
      <Closed />
    </Page>
  )
}

/**
 * A first read that did not come back. A refusal is the closed door rather
 * than a red box; anything else says what failed and offers the read again.
 */
export function ReadFailed({ error, onRetry }: { error: Error; onRetry: () => void }) {
  if (error instanceof ApiError && error.status === 403) return <Closed />
  return <ErrorState error={error} onRetry={onRetry} retryable />
}

import { RotateClockwise } from "@/components/icons"
import { Logo } from "@/components/logo"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"

/**
 * A whole window with no shell to stand in: the address or the error is above
 * the dashboard's layout, so there is no rail to name the page or to leave by.
 * It says what happened and carries its own way back, on the sign-in page's
 * ground. Nothing in it reads a provider, because `global-error` has none.
 */
export function StandaloneState({
  eyebrow,
  title,
  description,
  children,
  actions,
}: {
  eyebrow: string
  title: string
  description: string
  children?: React.ReactNode
  actions: React.ReactNode
}) {
  return (
    <div className="auth-backdrop flex min-h-svh flex-col bg-background">
      <main className="mx-auto flex w-full max-w-xl flex-1 flex-col justify-center gap-6 px-4 py-6 sm:px-6 sm:py-10">
        <Logo size="md" />
        <div className="space-y-1.5">
          <p className="eyebrow">{eyebrow}</p>
          <h1 className="text-2xl leading-tight font-semibold tracking-tight text-balance">
            {title}
          </h1>
          <p className="text-body leading-relaxed text-pretty text-muted-foreground">
            {description}
          </p>
        </div>
        {children}
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      </main>
    </div>
  )
}

/**
 * The window a render error above the shell leaves, for `error.tsx` and
 * `global-error.tsx`. The way out is a real navigation rather than a router
 * link: whatever threw may be the layout a link would land back in, and a full
 * load is the one thing that starts all of it again.
 */
export function RenderFailure({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  return (
    <>
      <title>Something went wrong · Just Dashboard</title>
      <StandaloneState
        eyebrow="Error"
        title="The dashboard stopped rendering"
        description="This is a bug in the dashboard rather than a problem with your server. Trying again is worth one attempt — most of these come from a single bad response that the next request replaces."
        actions={
          <>
            <Button size="sm" onClick={reset}>
              <RotateClockwise className="size-3.5" />
              Try again
            </Button>
            <Button size="sm" variant="outline" asChild>
              {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- see above */}
              <a href="/">Back to the Overview</a>
            </Button>
          </>
        }
      >
        <Well className="whitespace-pre-wrap">
          {error.message}
          {error.digest ? `\n\ndigest: ${error.digest}` : ""}
        </Well>
      </StandaloneState>
    </>
  )
}

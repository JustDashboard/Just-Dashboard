"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState } from "@/components/state"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { ProductLogos, hasProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"

/**
 * What a section shows where the tool it reads is not installed: what the
 * tool would do here, drawn as the product it is, and the package to install
 * — through the Packages page's own install route, whose job streams into a
 * console under the button so the reader watches it go in rather than
 * waiting on a spinner. When the job succeeds the section reads again.
 *
 * The button is an administrator's: installing a package runs its
 * maintainer's scripts as root (the install route's own capability), so
 * anybody else is told the package's name and nothing to press.
 */
export function InstallHandoff({
  pkg,
  products,
  icon,
  title,
  description,
  onInstalled,
}: {
  pkg: string
  /** The tool drawn as itself, where the logo collection has it. */
  products: string[]
  /** The glyph for a tool no logo names; used when `products` draws nothing. */
  icon?: React.ComponentType<{ className?: string }>
  title: string
  description: React.ReactNode
  onInstalled: () => void
}) {
  const { can } = useAuth()
  const console_ = useJobConsole({ onSuccess: onInstalled })
  const [starting, setStarting] = useState(false)
  const install = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/packages/install", { packages: [pkg] }))
    } catch (err) {
      notify.error(`Could not start installing ${pkg}`, err)
    } finally {
      setStarting(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <EmptyState
        title={title}
        icon={icon}
        mark={products.some(hasProductLogo) ? <ProductLogos ids={products} /> : undefined}
        description={description}
        action={
          can("system.admin") ? (
            <Button
              size="sm"
              onClick={() => void install()}
              pending={starting || console_.running}
              disabled={starting || console_.running}
            >
              Install {pkg}
            </Button>
          ) : (
            <span className="text-hint text-muted-foreground">
              An administrator can install <span className="font-mono">{pkg}</span>.
            </span>
          )
        }
      />
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
    </div>
  )
}

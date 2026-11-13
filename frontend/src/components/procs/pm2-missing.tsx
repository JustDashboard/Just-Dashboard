"use client"

import Link from "next/link"
import { ChartActivity, Terminal } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { ProductLogos, hasProductLogo } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * Where no account on this server has PM2. It is an npm package, installed
 * for the account whose applications it will run, so it is not something the
 * server's package manager can put in as it does git or WireGuard: the block
 * names the one command and opens the terminal it is typed in.
 */
export function PM2Missing() {
  const { can } = useAuth()
  return (
    <EmptyState
      icon={ChartActivity}
      mark={hasProductLogo("pm2") ? <ProductLogos ids={["pm2"]} /> : undefined}
      title="PM2 is not installed"
      description={
        <>
          PM2 comes from npm, for the account whose applications it runs:{" "}
          <span className="font-mono text-foreground">npm install -g pm2</span>. The dashboard finds
          every account&apos;s daemon on its own once one is started.
        </>
      }
      action={
        can("terminal") ? (
          <Button size="sm" variant="outline" asChild>
            <Link href="/terminal">
              <Terminal className="size-4" />
              Open a terminal
            </Link>
          </Button>
        ) : undefined
      }
    />
  )
}

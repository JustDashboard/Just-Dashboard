"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { Database, Eye, EyeOff, Copy } from "@/components/icons"
import type { DbConnection } from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { Field, FormFact, FormFacts } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { sectionHref } from "@/components/database/engine"
import { SidePanelFooter, useInSidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { useCopy } from "@/hooks/use-copy"

export type CreatedDatabase = { connection: DbConnection; url: string; reference?: string }

/** The connection is only offered after adoption and a fresh ping both succeeded. */
export function DatabaseReady({
  created,
  target = "host",
  canConnect = true,
  onConnect,
  onReset,
}: {
  created: CreatedDatabase
  target?: "host" | "container"
  canConnect?: boolean
  onConnect?: (connection: DbConnection, url: string) => void
  onReset: () => void
}) {
  const id = useId()
  const [revealed, setRevealed] = useState(false)
  const { copy } = useCopy()
  const { connection } = created
  const address = connection.host
    ? `${connection.host}${connection.port ? `:${connection.port}` : ""}`
    : undefined
  return (
    <Panel plain className="animate-rise">
      <PanelHeader title={`${connection.name} is ready`} />
      <PanelBody className="space-y-5">
        {/* The ping answered before this panel could be drawn, so the state
              is a reading, not a hope. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          <ProductLogo size="sm" id={connection.driver} fallback={Database} />
          <div className="min-w-0 flex-1 space-y-0.5">
            <p className="truncate text-body font-medium">{connection.name}</p>
            {(address || connection.database) && (
              <FormFacts>
                {address && (
                  <FormFact label="Host" mono>
                    {address}
                  </FormFact>
                )}
                {connection.database && (
                  <FormFact label="Database" mono>
                    {connection.database}
                  </FormFact>
                )}
              </FormFacts>
            )}
          </div>
          {/* The one reading this panel exists for, so a phone keeps it —
                under the name when the line has no room beside it. */}
          <Status
            tone="running"
            label="Accepting connections"
            className="max-sm:basis-full max-sm:pl-11"
          />
        </div>
        <Field
          label="Connection string"
          htmlFor={id}
          hint={
            target === "container"
              ? "Use this database to connect the application over its managed private network. The saved connection follows replacement database containers."
              : "This address is for processes on the host. Use Add database during project setup to get the address for an application container."
          }
        >
          <InputGroup>
            <InputGroupInput
              id={id}
              type={revealed ? "text" : "password"}
              readOnly
              value={created.url}
              className="font-mono"
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              <InputGroupButton
                aria-label={revealed ? "Hide connection string" : "Reveal connection string"}
                onClick={() => setRevealed(!revealed)}
              >
                {revealed ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </InputGroupButton>
              <InputGroupButton
                aria-label="Copy connection string"
                onClick={() => copy(created.url, "Connection string copied")}
              >
                <Copy className="size-3.5" />
                <span className="max-sm:hidden">Copy</span>
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
        </Field>
      </PanelBody>
      <Foot className="justify-between">
        <Button variant="outline" asChild>
          <Link href={sectionHref(connection.id)}>Open in Databases</Link>
        </Button>
        {onConnect ? (
          <Button
            disabled={!canConnect}
            onClick={() => onConnect(connection, created.reference || created.url)}
          >
            Use this database
          </Button>
        ) : (
          <Button onClick={onReset}>
            <Database className="size-4" />
            Create another
          </Button>
        )}
      </Foot>
    </Panel>
  )
}

function Foot({ className, children }: { className?: string; children: React.ReactNode }) {
  return useInSidePanel() ? (
    <SidePanelFooter>{children}</SidePanelFooter>
  ) : (
    <PanelFooter className={className}>{children}</PanelFooter>
  )
}

"use client"

import type { RailpackEvidence } from "@/lib/types"
import { plural } from "@/lib/format"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Tag } from "@/components/tag"
import { GroupRule } from "@/components/flow"
import { ShellWords } from "@/components/deploy/run-evidence"
import { railpackProviderLabel } from "@/components/deploy/vocabulary"
import {
  railpackImages,
  railpackNotices,
  railpackPackageLine,
} from "@/components/deploy/railpack-model"

/**
 * What Railpack decided for a run (`deploy/build_railpack.go`), as an
 * operator asks it: which release planned it and which provider it took, the
 * language releases it resolved, the steps its plan runs and how it starts,
 * and what it builds from. Railpack's own lines come first when it refused.
 */
export function RailpackView({ evidence }: { evidence: RailpackEvidence }) {
  const notices = railpackNotices(evidence)
  const provider = evidence.provider ?? evidence.providers[0]
  return (
    <section aria-label="What Railpack decided" className="min-w-0 space-y-5">
      <StatGrid columns={4} dense>
        <StatTile label="Railpack" value={evidence.version} hint="pinned by digest" />
        <StatTile
          label="Provider"
          value={provider ? railpackProviderLabel(provider) : "None found"}
          tone={provider ? "default" : "danger"}
          hint={evidence.provider ? "set in Build settings" : "Railpack's detection"}
        />
        <StatTile
          label="Packages"
          value={evidence.packages.length}
          hint={evidence.packages[0] ? railpackPackageLine(evidence.packages[0]).name : "none"}
        />
        <StatTile
          label="Plan"
          value={evidence.success ? "Written" : "Refused"}
          tone={evidence.success ? "success" : "danger"}
          hint={plural(evidence.steps.length, "step")}
        />
      </StatGrid>

      {notices.length > 0 && (
        <ul aria-label="Railpack's warnings" className="space-y-1.5">
          {notices.map((notice, index) => (
            <li key={index} className="flex min-w-0 items-start gap-2 text-body">
              <Tag tone={notice.level === "error" ? "danger" : "warning"}>
                {notice.level === "error" ? "error" : "warning"}
              </Tag>
              <span className="min-w-0 wrap-anywhere whitespace-pre-line">{notice.message}</span>
            </li>
          ))}
        </ul>
      )}

      {evidence.packages.length > 0 && (
        <section aria-label="Packages Railpack resolved" className="min-w-0 space-y-2">
          <GroupRule label="Packages" />
          <ul className="space-y-1">
            {evidence.packages.map((pkg) => {
              const line = railpackPackageLine(pkg)
              return (
                <li
                  key={pkg.name}
                  className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-body"
                >
                  <span className="font-mono">{line.name}</span>
                  {line.asked && (
                    <span className="text-hint text-muted-foreground">{line.asked}</span>
                  )}
                </li>
              )
            })}
          </ul>
        </section>
      )}

      {(evidence.steps.length > 0 || evidence.startCommand) && (
        <section aria-label="Railpack's plan" className="min-w-0 space-y-2">
          <GroupRule label="Plan" />
          <ol className="space-y-2">
            {evidence.steps.map((step) => (
              <li key={step.name} className="min-w-0 space-y-0.5">
                <p className="font-mono text-xs text-muted-foreground">{step.name}</p>
                {step.commands.map((command, index) => (
                  <p key={index} className="pl-3 font-mono text-xs wrap-anywhere">
                    <ShellWords command={command} />
                  </p>
                ))}
              </li>
            ))}
            {evidence.startCommand && (
              <li className="min-w-0 space-y-0.5">
                <p className="font-mono text-xs text-muted-foreground">start</p>
                <p className="pl-3 font-mono text-xs wrap-anywhere">
                  <ShellWords command={evidence.startCommand} />
                </p>
              </li>
            )}
          </ol>
        </section>
      )}

      {evidence.images.length > 0 && (
        <section aria-label="Images the plan builds from" className="min-w-0 space-y-2">
          <GroupRule label="Built from" />
          <ul className="space-y-1">
            {railpackImages(evidence).map(({ image, role, mise }) => (
              <li key={image} className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-body">
                <span className="font-mono text-xs wrap-anywhere">{image}</span>
                <span className="text-hint text-muted-foreground">
                  {role
                    ? `Railpack's ${role}, tagged by mise ${mise}`
                    : "named by the plan, by tag"}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </section>
  )
}

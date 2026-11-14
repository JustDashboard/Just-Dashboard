"use client"

import { createElement } from "react"
import {
  BookOpen,
  Copy,
  Download,
  External,
  Puzzle,
  SettingsGear,
  Terminal,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import type { InstalledPackage, PackageDetail, PackageUsage } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction, ROW_REVEAL } from "@/components/icon-action"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo, hasProductLogo, unitProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { dependency, samePackage, type PackageTab } from "@/components/packages/inventory"
import { OriginFact, PackageMark, packageProduct, sectionGlyph } from "@/components/packages/marks"

/** How many of what a package needs the picture names before it counts the rest. */
const NEEDS_SHOWN = 3

/** A package on a picture's tile: the product it is, or the glyph of its section. */
function PackageWireMark({
  name,
  section,
  size,
}: {
  name: string
  section?: string
  size: "lg" | "md" | "sm"
}) {
  const product = packageProduct(name)
  return (
    <WireMark tone="logo" size={size}>
      {hasProductLogo(product) ? (
        <ProductGlyph id={product} />
      ) : (
        createElement(sectionGlyph(section), { "aria-hidden": true })
      )}
    </WireMark>
  )
}

/** What a package needs, each named once: `debconf (>= 0.5) | debconf-2.0` is debconf. */
export function needsOf(detail: Pick<PackageDetail, "dependencies">) {
  const seen = new Set<string>()
  return (detail.dependencies ?? []).map(dependency).filter((dep) => {
    if (!dep.name || seen.has(dep.name)) return false
    seen.add(dep.name)
    return true
  })
}

/** How many things a package's file list says it put on the host. */
export function filesCount(usage: PackageUsage | null | undefined) {
  if (!usage) return 0
  return (
    (usage.commands?.length ?? 0) +
    (usage.services?.length ?? 0) +
    (usage.configFiles?.length ?? 0) +
    (usage.docs?.length ?? 0)
  )
}

/**
 * Where a package sits: what it needs on the left, the package in the middle,
 * what it gives the server on the right. The sheet said the two halves as two
 * lists a screen apart; the question a package is opened with (what did this
 * put here, and what did it pull in) is one picture.
 *
 * A line is still: nothing travels between packages. It is dashed to a
 * dependency that is not installed, and every line carries a pulse while the
 * manager is installing or removing this package.
 */
export function PackagePicture({
  detail,
  usage,
  usageError,
  manager,
  installedOf,
  inventoryRead,
  moving,
  hrefFor,
  onTab,
}: {
  /** Nothing until the first read lands: the picture is drawn at its height meanwhile. */
  detail?: PackageDetail
  usage: PackageUsage | null
  usageError?: string
  manager?: string
  installedOf: (name: string) => InstalledPackage | undefined
  /** Whether the inventory has landed, so "not installed" is a reading and not a guess. */
  inventoryRead: boolean
  moving: boolean
  hrefFor: (name: string) => string
  onTab: (tab: PackageTab) => void
}) {
  const needs = detail ? needsOf(detail) : []
  const start: WireFlowNode[] = needs.map((dep) => {
    const row = installedOf(dep.name)
    return {
      id: `needs:${dep.name}`,
      mark: (size) => <PackageWireMark name={dep.name} section={row?.section} size={size} />,
      title: <span className="block truncate font-mono">{dep.name}</span>,
      hint: (
        <span className="block truncate font-mono">
          {row ? row.version : inventoryRead ? "not installed" : " "}
        </span>
      ),
      name: dep.name,
      href: hrefFor(dep.name),
      label: `Open ${dep.name}`,
      line: { state: row || !inventoryRead ? "up" : "idle", moving },
    }
  })

  const gives: WireFlowNode[] = []
  const line = { state: "up", moving } as const
  const glyph = (Glyph: React.ComponentType<{ "aria-hidden"?: boolean }>) =>
    function Mark(size: "lg" | "md" | "sm") {
      return (
        <WireMark tone="logo" size={size}>
          <Glyph aria-hidden />
        </WireMark>
      )
    }
  if (detail?.installed && usage) {
    const commands = usage.commands ?? []
    if (commands.length > 0)
      gives.push({
        id: "gives:commands",
        mark: glyph(Terminal),
        eyebrow: commands.length === 1 ? "Command" : "Commands",
        title:
          commands.length === 1 ? (
            <span className="block truncate font-mono">{commands[0]}</span>
          ) : (
            plural(commands.length, "command")
          ),
        hint:
          commands.length === 1 ? (
            "run it from a terminal"
          ) : (
            <span className="block truncate font-mono">{commands.slice(0, 3).join(", ")}</span>
          ),
        name: plural(commands.length, "command"),
        onPress: () => onTab("files"),
        label: `Show its ${plural(commands.length, "command")}`,
        line,
      })
    const services = usage.services ?? []
    if (services.length > 0) {
      const product = unitProduct(services[0])
      gives.push({
        id: "gives:services",
        mark: (size) => (
          <WireMark tone="logo" size={size}>
            {hasProductLogo(product) ? <ProductGlyph id={product} /> : <SettingsGear aria-hidden />}
          </WireMark>
        ),
        eyebrow: services.length === 1 ? "Service" : "Services",
        title:
          services.length === 1 ? (
            <span className="block truncate font-mono">{services[0]}</span>
          ) : (
            plural(services.length, "service")
          ),
        hint:
          services.length === 1 ? (
            "a unit systemd runs"
          ) : (
            <span className="block truncate font-mono">{services.slice(0, 2).join(", ")}</span>
          ),
        name: services.length === 1 ? services[0] : plural(services.length, "service"),
        // One unit is a place to go; several are a list, which Files holds.
        ...(services.length === 1
          ? {
              href: `/processes/services/${encodeURIComponent(services[0])}`,
              label: `Open ${services[0]}`,
            }
          : {
              onPress: () => onTab("files"),
              label: `Show its ${plural(services.length, "service")}`,
            }),
        line,
      })
    }
    const config = usage.configFiles ?? []
    if (config.length > 0)
      gives.push({
        id: "gives:config",
        mark: glyph(SettingsGear),
        eyebrow: "Configuration",
        title:
          config.length === 1 ? (
            <span className="block truncate font-mono">{config[0]}</span>
          ) : (
            plural(config.length, "file")
          ),
        hint:
          config.length === 1 ? (
            "the file it reads"
          ) : (
            <span className="block truncate font-mono">{config[0]}</span>
          ),
        name: plural(config.length, "configuration file"),
        onPress: () => onTab("files"),
        label: `Show its ${plural(config.length, "configuration file")}`,
        line,
      })
    if (usage.manual || usage.manUnavailable || (usage.manPages?.length ?? 0) > 0)
      gives.push({
        id: "gives:manual",
        mark: glyph(BookOpen),
        eyebrow: "Manual",
        title: (
          <span className="block truncate font-mono">
            man {usage.manualFor ?? usage.manPages?.[0]?.name ?? detail.name}
          </span>
        ),
        hint: plural(Math.max(usage.manPages?.length ?? 0, 1), "page"),
        name: "Manual",
        onPress: () => onTab("manual"),
        label: "Read its manual",
        line,
      })
  }

  const nothing = !detail
    ? null
    : !detail.installed
      ? { title: "Not installed", hint: "Install it to see what it gives this server" }
      : !usage
        ? { title: "Not read", hint: usageError || "Its file list could not be read" }
        : { title: "Nothing to run", hint: "A library other packages are built on" }

  return (
    <WireFlow
      // The room four nodes take, whatever the package has: what is under
      // the picture is in one place for every package, and while one loads.
      className="lg:flex lg:min-h-[19.75rem] lg:flex-col lg:justify-center lg:pt-12"
      phone="marks"
      loading={!detail}
      start={{
        title: "Needs",
        heading: "always",
        count: needs.length || undefined,
        nodes: start,
        max: NEEDS_SHOWN,
        more: {
          onPress: () => onTab("depends"),
          words: (hidden) => `${hidden} more`,
        },
        loadingRows: 3,
        empty: (
          <WireNode
            align="end"
            mark={
              <WirePlaceholder size="md">
                <Puzzle />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Nothing else</span>}
            hint="It depends on no other package"
          />
        ),
      }}
      hub={{
        mark: (size) =>
          detail ? (
            <PackageWireMark name={detail.name} section={detail.section} size={size} />
          ) : (
            <WireMark tone="logo" size={size}>
              <Puzzle aria-hidden />
            </WireMark>
          ),
        eyebrow: manager ? `${manager} package` : "Package",
        title: <span className="block truncate font-mono">{detail?.name}</span>,
      }}
      end={{
        title: "Gives this server",
        heading: "always",
        nodes: gives,
        loadingRows: 3,
        empty: nothing && (
          <WireNode
            mark={
              <WirePlaceholder size="md">
                <Puzzle />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">{nothing.title}</span>}
            hint={nothing.hint}
          />
        ),
      }}
    />
  )
}

/** The picture, then why it cannot be removed where it cannot, then what it is. */
export function PackageOverview({
  detail,
  picture,
}: {
  detail: PackageDetail
  picture: React.ReactNode
}) {
  const about = detail.description || detail.summary
  const facts = Boolean(
    detail.repository ||
    detail.section ||
    detail.arch ||
    detail.license ||
    detail.maintainer ||
    detail.homepage,
  )
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {picture}
      {detail.protected && (
        <Notice title="This one cannot be removed from here">{detail.protected}</Notice>
      )}
      {(about || facts) && (
        <Panel plain>
          <PanelHeader title="About" />
          <PanelBody className="space-y-4">
            {about && (
              <p className="max-w-3xl text-body leading-relaxed whitespace-pre-line text-muted-foreground">
                {about}
              </p>
            )}
            {facts && (
              <DetailList>
                {detail.repository && (
                  <Detail label="Repository">
                    <OriginFact origin={detail.repository} />
                  </Detail>
                )}
                {detail.section && <Detail label="Section">{detail.section}</Detail>}
                {detail.arch && (
                  <Detail label="Architecture">
                    <span className="font-mono">{detail.arch}</span>
                  </Detail>
                )}
                {detail.license && <Detail label="Licence">{detail.license}</Detail>}
                {detail.maintainer && (
                  <Detail label="Maintainer">
                    <span className="break-all">{detail.maintainer}</span>
                  </Detail>
                )}
                {detail.homepage && (
                  <Detail label="Homepage">
                    <a
                      href={detail.homepage}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="inline-flex max-w-full min-w-0 items-center gap-1 text-primary hover:underline"
                    >
                      <span className="truncate">{detail.homepage}</span>
                      <External className="size-3 shrink-0" />
                    </a>
                  </Detail>
                )}
              </DetailList>
            )}
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

/** Copies a command, because the next thing anybody does with one is run it. */
function CommandChip({ command }: { command: string }) {
  return (
    <button
      type="button"
      aria-label={`Copy ${command}`}
      onClick={() => void copyText(command, `Copied ${command}`)}
      className="group inline-flex min-h-9 max-w-full min-w-0 items-center gap-1.5 rounded-md border border-hairline bg-control px-2 py-1 font-mono text-body focus-ring transition-colors hover:bg-control-hover active:bg-control-active"
    >
      <Terminal className="size-3 text-muted-foreground" />
      <span className="min-w-0 truncate">
        <ShellWords command={command} />
      </span>
      <Copy className={cn("size-3 text-muted-foreground", ROW_REVEAL)} />
    </button>
  )
}

/** A section of a view: its title, its count at the far end, a hairline. */
function ViewSection({
  title,
  count,
  children,
}: {
  title: string
  count?: number
  children: React.ReactNode
}) {
  return (
    <Panel plain>
      <PanelHeader
        title={title}
        actions={
          count !== undefined && (
            <span className="numeric text-hint text-muted-foreground">{count}</span>
          )
        }
      />
      <PanelBody className="space-y-3">{children}</PanelBody>
    </Panel>
  )
}

/** A package that is not on the host has no files to read; the next step is to put it there. */
function NotInstalled({ what, onInstall }: { what: string; onInstall?: () => void }) {
  return (
    <EmptyState
      icon={Puzzle}
      title="Not installed yet"
      description={`Install it to see ${what}.`}
      action={
        onInstall && (
          <Button size="sm" variant="outline" onClick={onInstall}>
            <Download className="size-4" />
            Install
          </Button>
        )
      }
    />
  )
}

/**
 * What a package put on this server, read from its own file list: the
 * commands it installed (almost never its own name), the units it registered,
 * the files it reads its settings from and the documentation beside them.
 * Nothing here is a hand-written table of blurbs, and nothing runs the
 * package's binaries to find out (`internal/updates/usage.go`).
 */
export function PackageFiles({
  usage,
  installed,
  error,
  onRetry,
  onInstall,
}: {
  usage: PackageUsage | null
  installed: boolean
  error?: string
  onRetry: () => void
  onInstall?: () => void
}) {
  if (!installed)
    return (
      <NotInstalled
        what="the commands, services and configuration files it provides"
        onInstall={onInstall}
      />
    )
  if (!usage)
    return (
      <ErrorState
        error={new Error(error || "Could not read this package’s commands and files")}
        onRetry={onRetry}
        retryable
      />
    )
  if (usage.empty || filesCount(usage) === 0)
    return (
      <EmptyState
        icon={Puzzle}
        title="Nothing to run"
        description="This package ships no commands, services or configuration: it is a library that other packages are built on."
      />
    )
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {usage.commands && usage.commands.length > 0 && (
        <ViewSection title="Commands" count={usage.commands.length}>
          <div className="flex flex-wrap gap-1.5">
            {usage.commands.map((command) => (
              <CommandChip key={command} command={command} />
            ))}
          </div>
        </ViewSection>
      )}

      {usage.services && usage.services.length > 0 && (
        <ViewSection title="Services" count={usage.services.length}>
          <ChoiceList>
            {usage.services.map((service) => {
              const start = `systemctl start ${service}`
              return (
                <ChoiceRow
                  key={service}
                  leading={
                    <ProductLogo id={unitProduct(service)} size="sm" fallback={SettingsGear} />
                  }
                  title={<span className="font-mono">{service}</span>}
                  description={<span className="font-mono">{start}</span>}
                  verb={`Open ${service}`}
                  href={`/processes/services/${encodeURIComponent(service)}`}
                  actions={
                    <IconAction
                      label={`Copy ${start}`}
                      onClick={() => void copyText(start, `Copied ${start}`)}
                    >
                      <Copy />
                    </IconAction>
                  }
                />
              )
            })}
          </ChoiceList>
        </ViewSection>
      )}

      {usage.configFiles && usage.configFiles.length > 0 && (
        <ViewSection title="Configuration" count={usage.configFiles.length}>
          <FileRows files={usage.configFiles} />
        </ViewSection>
      )}

      {usage.docs && usage.docs.length > 0 && (
        <ViewSection title="Documentation on this machine" count={usage.docs.length}>
          <FileRows files={usage.docs} />
        </ViewSection>
      )}
    </div>
  )
}

/** Files, each opening where it is in the file manager. */
function FileRows({ files }: { files: string[] }) {
  return (
    <ChoiceList>
      {files.map((file) => (
        <ChoiceRow
          key={file}
          title={<span className="font-mono">{file}</span>}
          verb={`Open ${file}`}
          href={`/files?path=${encodeURIComponent(file)}`}
        />
      ))}
    </ChoiceList>
  )
}

/** The manual page the package ships, as `man` prints it. */
export function PackageManual({
  usage,
  installed,
  error,
  onRetry,
  onInstall,
}: {
  usage: PackageUsage | null
  installed: boolean
  error?: string
  onRetry: () => void
  onInstall?: () => void
}) {
  if (!installed) return <NotInstalled what="its manual" onInstall={onInstall} />
  if (!usage)
    return (
      <ErrorState
        error={new Error(error || "Could not read this package’s manual")}
        onRetry={onRetry}
        retryable
      />
    )
  const pages = usage.manPages ?? []
  if (!usage.manual && !usage.manUnavailable && pages.length === 0)
    return (
      <EmptyState
        icon={BookOpen}
        title="No manual page"
        description="This package ships no page for man to print."
      />
    )
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {usage.manUnavailable && (
        <Notice title="The manual could not be read">{usage.manUnavailable}</Notice>
      )}
      {usage.manual && (
        <ViewSection title={`man ${usage.manualFor}`}>
          {/* whitespace-pre, not pre-wrap: a man page's two columns are made
              of spaces, and wrapping them folds the description under the flag
              it belongs to. It scrolls sideways inside the well instead, which
              is the rule every wide block in this product follows. */}
          <Well className="overflow-auto p-3 max-xl:max-h-[32rem]">
            <pre className="font-mono text-hint leading-relaxed whitespace-pre">{usage.manual}</pre>
          </Well>
          {usage.truncated && (
            <p className="text-hint text-muted-foreground">
              Cut off here. The rest is in `man {usage.manualFor}` from a terminal.
            </p>
          )}
        </ViewSection>
      )}
      {pages.length > 1 && (
        <ViewSection title="Other manual pages" count={pages.length}>
          <div className="flex flex-wrap gap-1">
            {pages.map((page) => (
              <Tag key={page.path} mono title={page.path}>
                {page.name}({page.section})
              </Tag>
            ))}
          </div>
        </ViewSection>
      )}
    </div>
  )
}

/**
 * Everything a package needs, one line each: the package as the software it
 * is, whether it is on the host and at what version, and what was asked of
 * it. The sheet drew the first forty as chips and counted the rest.
 */
export function PackageDepends({
  detail,
  installedOf,
  inventoryRead,
  hrefFor,
}: {
  detail: PackageDetail
  installedOf: (name: string) => InstalledPackage | undefined
  inventoryRead: boolean
  hrefFor: (name: string) => string
}) {
  const needs = needsOf(detail)
  if (needs.length === 0)
    return (
      <EmptyState
        icon={Puzzle}
        title="Needs nothing else"
        description="This package depends on no other package."
      />
    )
  return (
    <ChoiceList aria-label={`What ${detail.name} depends on`}>
      {needs.map((dep) => {
        const row = installedOf(dep.name)
        return (
          <ChoiceRow
            key={dep.name}
            workspaceItem={{ id: dep.name, name: dep.name }}
            leading={<PackageMark name={dep.name} section={row?.section} />}
            title={<span className="font-mono">{dep.name}</span>}
            description={
              <span className="font-mono">
                {row ? row.version : inventoryRead ? "not installed" : " "}
              </span>
            }
            verb={`Open ${dep.name}`}
            href={hrefFor(dep.name)}
            trailing={
              dep.wants && (
                <span
                  className="max-w-28 truncate font-mono text-hint text-muted-foreground sm:max-w-64"
                  title={dep.wants}
                >
                  {dep.wants}
                </span>
              )
            }
          />
        )
      })}
    </ChoiceList>
  )
}

/** The installed row a name answers to, whichever of apt's two spellings it is in. */
export function installedLookup(packages: InstalledPackage[] | undefined) {
  const byName = new Map((packages ?? []).map((p) => [p.name.split(":")[0], p]))
  return (name: string) => {
    const row = byName.get(name.split(":")[0])
    return row && samePackage(row.name, name) ? row : undefined
  }
}

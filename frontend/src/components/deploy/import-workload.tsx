"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  Box,
  Check,
  Cpu,
  Layers,
  RefreshClockwise,
  Terminal,
  Warning,
} from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  WORKLOAD_KINDS,
  WORKLOAD_LABELS,
  workloadManagerUrl,
  workloadMatches,
  workloadPort,
  type WorkloadCandidate,
  type WorkloadDiscovery,
  type WorkloadKind,
  type WorkloadScope,
} from "@/lib/workload-import"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentDraft } from "@/lib/types"
import { Field, FormSection } from "@/components/form"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import {
  ChoiceList,
  ChoiceRow,
  FlowActions,
  FlowHeader,
  FlowPanel,
  FlowPanelBody,
  FlowPanelHeader,
  FlowSteps,
} from "@/components/flow"
import { Page, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { deploymentName } from "@/components/deploy/vocabulary"

const STEPS = [
  { key: "discover", label: "Discover" },
  { key: "recover", label: "Recover settings" },
  { key: "review", label: "Review migration" },
]

const MARKS = { stack: Layers, container: Box, pm2: Terminal, systemd: Terminal, process: Cpu }

function asError(value: unknown) {
  return value instanceof Error ? value : new Error(String(value))
}

function WorkloadMark({ item }: { item: WorkloadCandidate }) {
  const image = item.services.find((service) => service.image)?.image
  return (
    <ProductLogo
      id={image ? imageProduct(image) : undefined}
      fallback={MARKS[item.kind]}
      size="sm"
    />
  )
}

/** Recover a server draft first; adopting its reviewed baseline never starts a deployment run. */
export function ImportWorkload() {
  const router = useRouter()
  const { can } = useAuth()
  const discovery = usePoll(
    (signal) => get<WorkloadDiscovery>("/deploy/import/discovery", undefined, signal),
    0,
  )
  const [query, setQuery] = useState("")
  const [kind, setKind] = useState<WorkloadKind | "all">("all")
  const [selected, setSelected] = useState<WorkloadCandidate>()
  const [name, setName] = useState("")
  const [scope, setScope] = useState<WorkloadScope>("all_services")
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  const [stale, setStale] = useState(false)
  const heading = useRef<HTMLDivElement>(null)
  const pending = useRef(false)
  const step = selected ? 1 : 0
  const items = useMemo(() => discovery.data?.items ?? [], [discovery.data])
  const filtered = items.filter(
    (item) => (kind === "all" || item.kind === kind) && workloadMatches(item, query),
  )
  const managerUrl = selected && workloadManagerUrl(selected)
  const nameError =
    failure instanceof ApiError && (failure.field === "name" || failure.code === "name_taken")
      ? failure.message
      : undefined

  useEffect(() => {
    if (step > 0) {
      const title = heading.current?.querySelector("h1")
      title?.setAttribute("tabindex", "-1")
      title?.focus()
    }
  }, [step])

  const inspect = async (item: WorkloadCandidate, preserveName = false) => {
    if (pending.current) return
    pending.current = true
    setBusy(item.key)
    setFailure(undefined)
    try {
      const fresh = await post<WorkloadCandidate>("/deploy/import/inspect", { key: item.key })
      setSelected(fresh)
      if (!preserveName) {
        setName(deploymentName(fresh.name))
        setScope("all_services")
      }
      setStale(false)
    } catch (error) {
      setFailure(asError(error))
      if (preserveName) setStale(true)
    } finally {
      setBusy("")
      pending.current = false
    }
  }

  const recover = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!selected || !name.trim() || stale || pending.current) return
    pending.current = true
    setBusy("recover")
    setFailure(undefined)
    try {
      const result = await post<DeploymentDraft>("/deploy/import/recover", {
        key: selected.key,
        name: name.trim(),
        digest: selected.digest,
        ...(selected.kind === "stack" ? { scope } : {}),
      })
      if (!result.data?.adoption || !result.data.source || !result.data.configuration)
        throw new Error(
          "The settings could not be recovered. Check the original manager and try again.",
        )
      if (result.data.adoption.blockers?.length)
        throw new Error(result.data.adoption.blockers.join(" "))
      router.push(`/deploy/new?draft=${encodeURIComponent(result.id)}`)
    } catch (error) {
      const refusal = asError(error)
      setFailure(refusal)
      // A conflict must be reviewed again before a fresh digest is submitted.
      if (refusal instanceof ApiError && refusal.code === "workload_changed") {
        setStale(true)
      }
    } finally {
      setBusy("")
      pending.current = false
    }
  }

  const chooseAnother = () => {
    setSelected(undefined)
    setFailure(undefined)
    setStale(false)
    discovery.refresh()
  }

  return (
    <Page register="flow" fill="xl">
      <div ref={heading} className="shrink-0 [&_h1]:outline-none" tabIndex={-1}>
        <FlowHeader
          eyebrow={
            <Link
              href="/deploy"
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <ArrowLeft aria-hidden className="size-3" /> Deployments · Import existing
            </Link>
          }
          question={selected ? "Recover this workload's settings?" : "What is already running?"}
          steps={<FlowSteps steps={STEPS} current={step} />}
        />
      </div>

      <div className="grid min-w-0 gap-6 xl:min-h-0 xl:flex-1 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
        <FlowPanel className="xl:max-h-full xl:min-h-0 xl:self-start">
          {!selected && (
            <>
              <FlowPanelHeader
                title="Workloads on this server"
                actions={
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={Boolean(busy)}
                    pending={discovery.loading}
                    onClick={discovery.refresh}
                  >
                    <RefreshClockwise aria-hidden className="size-3.5" /> Refresh
                  </Button>
                }
              />
              <FlowPanelBody className="flex min-h-0 flex-1 flex-col gap-4">
                {discovery.data && (
                  <StatGrid columns={3} dense>
                    <StatTile label="Workloads found" value={items.length} />
                    <StatTile
                      label="Services running"
                      value={items.reduce((sum, item) => sum + item.running, 0)}
                    />
                    <StatTile
                      label="Services not running"
                      value={items.reduce(
                        (sum, item) => sum + Math.max(0, item.total - item.running),
                        0,
                      )}
                    />
                  </StatGrid>
                )}
                <SearchInput
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Search name, image or port"
                  aria-label="Search workloads"
                  containerClassName="shrink-0 max-sm:basis-auto sm:w-full"
                />
                <ChipStrip className="shrink-0" aria-label="Workload kinds">
                  <FilterChip selected={kind === "all"} onClick={() => setKind("all")}>
                    All <ChipCount>{items.length}</ChipCount>
                  </FilterChip>
                  {WORKLOAD_KINDS.map((option) => {
                    const count = items.filter((item) => item.kind === option.key).length
                    return count > 0 || kind === option.key ? (
                      <FilterChip
                        key={option.key}
                        selected={kind === option.key}
                        onClick={() => setKind(option.key)}
                      >
                        {option.label} <ChipCount>{count}</ChipCount>
                      </FilterChip>
                    ) : null
                  })}
                </ChipStrip>
                <div className="-mx-3 max-h-[min(60vh,40rem)] overflow-y-auto px-3 xl:max-h-none xl:min-h-0 xl:flex-1">
                  {discovery.loading && !discovery.data && <LoadingRows />}
                  {discovery.error && (
                    <ErrorState error={discovery.error} onRetry={discovery.refresh} />
                  )}
                  {failure && (
                    <div role="alert" className="mb-3">
                      <ErrorState error={failure} />
                    </div>
                  )}
                  {discovery.data && filtered.length === 0 && (
                    <EmptyNote>
                      {items.length === 0
                        ? "No importable workloads were found. Check the discovery coverage beside this list."
                        : "No workloads match. Change the filter or search."}
                    </EmptyNote>
                  )}
                  <ChoiceList aria-label="Discovered workloads">
                    {filtered.map((item, index) => (
                      <ChoiceRow
                        key={item.key}
                        index={index}
                        title={item.name}
                        verb={`Review ${item.name}`}
                        onSelect={() => void inspect(item)}
                        disabled={Boolean(busy) && busy !== item.key}
                        busy={busy === item.key}
                        leading={<WorkloadMark item={item} />}
                        description={
                          busy === item.key
                            ? "Inspecting current workload…"
                            : WORKLOAD_LABELS[item.kind]
                        }
                        trailing={
                          <span className="numeric text-hint text-muted-foreground">
                            {item.running}/{item.total} running
                          </span>
                        }
                      >
                        {item.warnings.length > 0 && (
                          <span className="flex items-center gap-1.5 text-hint text-warning">
                            <Warning aria-hidden className="size-3.5" />{" "}
                            {plural(item.warnings.length, "review note")}
                          </span>
                        )}
                      </ChoiceRow>
                    ))}
                  </ChoiceList>
                </div>
              </FlowPanelBody>
            </>
          )}

          {selected && (
            <form onSubmit={(event) => void recover(event)} className="flex min-h-0 flex-col">
              <FlowPanelHeader
                title={selected.name}
                actions={
                  <Status
                    tone={
                      selected.running === selected.total
                        ? "running"
                        : selected.running > 0
                          ? "warning"
                          : "stopped"
                    }
                    label={`${selected.running}/${selected.total} running`}
                  />
                }
              />
              <FlowPanelBody className="max-h-[min(65vh,44rem)] space-y-5 overflow-y-auto xl:max-h-none xl:min-h-0 xl:flex-1">
                <Field
                  label="Project name"
                  htmlFor="import-project-name"
                  hint="The name used in Deployments. The workload keeps its original name."
                  error={nameError}
                >
                  <Input
                    id="import-project-name"
                    value={name}
                    maxLength={64}
                    required
                    disabled={Boolean(busy)}
                    aria-invalid={Boolean(nameError)}
                    onChange={(event) => {
                      setName(event.target.value)
                      if (nameError) setFailure(undefined)
                    }}
                  />
                </Field>
                {selected.kind === "stack" && (
                  <FormSection
                    title="Recovery scope"
                    hint="Choose which services a later Deploy changes will manage. Adoption itself leaves running and stopped containers unchanged."
                  >
                    <ChoiceGrid columns={2} role="group" aria-label="Compose recovery scope">
                      <ChoiceCard
                        selected={scope === "all_services"}
                        disabled={Boolean(busy)}
                        aria-label="Every declared service"
                        onClick={() => setScope("all_services")}
                      >
                        <span className="text-body font-medium">Every declared service</span>
                        <span className="text-hint leading-relaxed text-muted-foreground">
                          Recover the full Compose recipe, including services without a container.
                        </span>
                      </ChoiceCard>
                      <ChoiceCard
                        selected={scope === "existing_services"}
                        disabled={Boolean(busy)}
                        aria-label="Existing containers only"
                        onClick={() => setScope("existing_services")}
                      >
                        <span className="text-body font-medium">Existing containers only</span>
                        <span className="text-hint leading-relaxed text-muted-foreground">
                          Keep running and stopped containers. Review the excluded services before
                          adoption.
                        </span>
                      </ChoiceCard>
                    </ChoiceGrid>
                  </FormSection>
                )}
                {failure && !nameError && (
                  <div role="alert">
                    <ErrorState error={failure} />
                  </div>
                )}
                {stale && (
                  <Notice title="Inspect the workload again" tone="warning">
                    Its configuration or identity changed after inspection. Review the latest
                    services before recovering its settings.
                  </Notice>
                )}
                <FormSection
                  title="Services"
                  hint={`${plural(selected.total, "service")} · ${selected.running} running · ${Math.max(0, selected.total - selected.running)} not running`}
                >
                  <RowList aria-label="Services to import">
                    {selected.services.map((service) => (
                      <Row
                        key={service.resourceId}
                        title={service.name}
                        leading={
                          <ProductLogo
                            id={service.image ? imageProduct(service.image) : undefined}
                            fallback={MARKS[selected.kind]}
                            size="sm"
                          />
                        }
                        subtitle={
                          service.image ?? (service.pid ? `PID ${service.pid}` : service.resourceId)
                        }
                        mono
                        trailing={
                          <>
                            <Status state={service.state} />
                            {service.health && (
                              <Status
                                tone={service.health === "healthy" ? "running" : "warning"}
                                label={service.health}
                              />
                            )}
                          </>
                        }
                      />
                    ))}
                  </RowList>
                </FormSection>
                {selected.services.some((service) => service.ports?.length) && (
                  <FormSection title="Published ports">
                    <dl className="space-y-2 text-xs">
                      {selected.services
                        .filter((service) => service.ports?.length)
                        .map((service) => (
                          <div
                            key={service.resourceId}
                            className="flex min-w-0 flex-wrap justify-between gap-x-4 gap-y-1"
                          >
                            <dt className="text-muted-foreground">{service.name}</dt>
                            <dd className="min-w-0 font-mono break-all">
                              {service.ports?.map(workloadPort).join(", ")}
                            </dd>
                          </div>
                        ))}
                    </dl>
                  </FormSection>
                )}
                <FormSection title="Original configuration">
                  <dl className="space-y-2 text-xs">
                    <div className="flex justify-between gap-4">
                      <dt className="text-muted-foreground">Manager</dt>
                      <dd>{WORKLOAD_LABELS[selected.kind]}</dd>
                    </div>
                    <div className="flex justify-between gap-4">
                      <dt className="text-muted-foreground">Configuration</dt>
                      <dd>
                        {selected.configurationAvailable
                          ? "Available in place"
                          : "Could not be recovered"}
                      </dd>
                    </div>
                    {selected.sourcePath && (
                      <div className="space-y-1">
                        <dt className="text-muted-foreground">Location</dt>
                        <dd className="font-mono break-all">{selected.sourcePath}</dd>
                      </div>
                    )}
                  </dl>
                  {!selected.configurationAvailable && (
                    <Notice title="Recovery needs review" tone="warning">
                      Recovery checks the running workload for a reproducible configuration. Missing
                      settings or an unsafe migration must be resolved before adoption.
                    </Notice>
                  )}
                </FormSection>
                {selected.warnings.length > 0 && (
                  <FormSection title="Review notes">
                    <ul className="space-y-2 text-xs leading-relaxed text-muted-foreground">
                      {selected.warnings.map((warning, index) => (
                        <li key={index} className="flex gap-2">
                          <Warning aria-hidden className="mt-0.5 size-3.5 shrink-0 text-warning" />
                          <span>{warning}</span>
                        </li>
                      ))}
                    </ul>
                  </FormSection>
                )}
              </FlowPanelBody>
              <FlowActions
                note="Recovers a draft for review. The existing workload keeps running."
                secondary={
                  <Button variant="ghost" disabled={Boolean(busy)} onClick={chooseAnother}>
                    Back
                  </Button>
                }
              >
                {stale ? (
                  <Button
                    type="button"
                    pending={busy === selected.key}
                    onClick={() => void inspect(selected, true)}
                  >
                    Inspect again
                  </Button>
                ) : (
                  <Button
                    type="submit"
                    pending={busy === "recover"}
                    disabled={!name.trim() || Boolean(busy) || !can("system.admin")}
                  >
                    Review migration <ArrowRight aria-hidden className="size-3.5" />
                  </Button>
                )}
              </FlowActions>
            </form>
          )}
        </FlowPanel>

        <div className="space-y-6 xl:min-h-0 xl:overflow-y-auto">
          <Panel plain>
            <PanelHeader title="What adoption preserves" />
            <PanelBody className="space-y-3 text-xs leading-relaxed text-muted-foreground">
              <p>
                Recovery captures the settings for a regular deployment. Review the configuration,
                data and migration warnings before adopting the current application.
              </p>
              <ul className="space-y-2">
                {[
                  "Containers, volumes and networks stay in place.",
                  "Ports and mounts are recovered; private environment values stay sealed on the server.",
                  "Compose files and process-manager settings stay where they are.",
                ].map((text) => (
                  <li key={text} className="flex gap-2">
                    <Check aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
                    <span>{text}</span>
                  </li>
                ))}
              </ul>
              <p>
                Adoption records the current live baseline without a restart. Deploy changes uses
                the reviewed settings and may require downtime to transfer the runtime. Redeploy
                live release restores the baseline instead.
              </p>
              {managerUrl && (
                <Button variant="outline" size="sm" asChild>
                  <Link href={managerUrl}>
                    Open original manager <ArrowRight aria-hidden className="size-3.5" />
                  </Link>
                </Button>
              )}
            </PanelBody>
          </Panel>
          <Panel plain>
            <PanelHeader title="Discovery coverage" />
            <PanelBody className="space-y-3 text-xs leading-relaxed text-muted-foreground">
              <p>
                Compose projects, standalone Docker containers, PM2 apps, systemd services and
                listening host processes are checked. Services already tracked by Deployments are
                excluded.
              </p>
              {discovery.data?.checkedAt && <p>Checked {relativeTime(discovery.data.checkedAt)}</p>}
              {discovery.data?.silences && discovery.data.silences.length > 0 && (
                <Notice title="Some sources could not be checked" tone="warning">
                  <ul className="space-y-2">
                    {discovery.data.silences.map((message, index) => (
                      <li key={index}>{message}</li>
                    ))}
                  </ul>
                </Notice>
              )}
              <p>
                Processes without a listening port, inaccessible managers and remote Docker hosts
                may need manual configuration.
              </p>
            </PanelBody>
          </Panel>
          {!can("system.admin") && (
            <Notice title="Administrator access required" tone="warning">
              An administrator can import workloads into Deployments.
            </Notice>
          )}
        </div>
      </div>
    </Page>
  )
}

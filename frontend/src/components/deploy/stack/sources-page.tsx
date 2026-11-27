"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Rss, Trash } from "@/components/icons"
import { ApiError, del, get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useSessionState } from "@/lib/view-state"
import type { SmartStackSource, SmartStackSourceCatalogue } from "@/lib/types"
import { ChipStrip, FilterChip } from "@/components/chips"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { DimActions } from "@/components/icon-action"
import { BackLink } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { SearchInput } from "@/components/search-input"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbMenu } from "@/components/verbs"
import { useProject } from "@/components/deploy/project-context"
import { SourceForm } from "@/components/deploy/stack/source-form"
import {
  DEFINITIONS_SHOWN,
  PRIVACY_FILTERS,
  PRIVACY_WORD,
  cutWords,
  definitionHref,
  definitionLine,
  filterDefinitions,
  privacyFilter,
  sourcesHref,
  sourcesRefusal,
  type PrivacyFilter,
} from "@/components/deploy/stack/sources-model"

/**
 * A stack's media sources, `/deploy/[id]/stack/sources`: what the stack
 * searches for films and series, and where another is added. The sources are
 * Prowlarr's, read and written through the dashboard so nobody has to open
 * Prowlarr to add one, and shared with Radarr and Sonarr when one is added.
 *
 * With nothing open it is the sources the stack has, then the catalogue to
 * add from, searched and filtered by Public or Private. A definition opens on
 * this page's own address (`?definition=`): its fields as a form and Add, the
 * outcome in words. From `xl` the catalogue stands beside it as a rail in the
 * same search and filter; on a phone the page is that one pane, with the way
 * back (detail-is-a-page-not-a-sheet, phone-one-pane).
 */
export function StackSourcesPage() {
  const project = useProject()
  const { can } = useAuth()
  const router = useRouter()
  const params = useSearchParams()
  const base = `/deploy/${project.projectId}`
  const definition = params.get("definition") ?? ""
  const filter = privacyFilter(params.get("filter"))
  const [query, setQuery] = useSessionState(`deploy.${project.projectId}.stack.sources.query`, "")
  const settled = !project.detail.deployment.sourceTemplate || project.blueprint !== undefined
  const named = Boolean(project.blueprint?.stack)

  const catalogue = usePoll(
    (signal) =>
      get<SmartStackSourceCatalogue>(`${base}/stack/sources/catalogue`, undefined, signal),
    0,
    [base],
    { enabled: named },
  )
  const definitions = catalogue.data?.definitions ?? []
  const matching = filterDefinitions(definitions, query, filter)
  const shown = matching.slice(0, DEFINITIONS_SHOWN)
  const setFilter = (next: PrivacyFilter) =>
    router.replace(definition ? definitionHref(base, definition, next) : sourcesHref(base, next), {
      scroll: false,
    })

  if (settled && !named)
    return (
      <EmptyState
        icon={Rss}
        title="This project is not a stack"
        description="Media sources belong to a media stack, made from the Stacks tab of a new deployment."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={base}>Open the Overview</Link>
          </Button>
        }
      />
    )

  const filters = (
    <ChipStrip role="group" aria-label="Which sources">
      {PRIVACY_FILTERS.map((one) => (
        <FilterChip
          key={one.value}
          selected={one.value === filter}
          onClick={() => setFilter(one.value)}
        >
          {one.label}
        </FilterChip>
      ))}
    </ChipStrip>
  )

  if (definition) {
    const listed = definitions.find((one) => one.definition === definition)
    return (
      <div className="grid min-w-0 items-start gap-x-6 gap-y-4 xl:grid-cols-[auto_minmax(0,1fr)]">
        {/* The item before the rail in the document, the rail first on screen. */}
        <div className="min-w-0 space-y-4 xl:col-start-2 xl:row-start-1">
          <p className="text-hint text-muted-foreground">
            <BackLink href={sourcesHref(base, filter)}>Media sources</BackLink>
          </p>
          <SourceForm
            key={definition}
            base={base}
            definition={definition}
            listed={listed}
            canAdd={can("service.control") && !project.archived}
            backHref={sourcesHref(base, filter)}
            stackHref={`${base}/stack`}
          />
        </div>
        <Rail
          slot="source-rail"
          label="Sources to add"
          className="xl:sticky xl:top-4 xl:col-start-1 xl:row-start-1 xl:max-h-[calc(100dvh-12rem)] xl:self-start"
          search={{
            label: "Search the sources",
            placeholder: "Name or description",
            query,
            onQuery: setQuery,
          }}
          filters={filters}
          caption={`${matching.length.toLocaleString("en")} ${matching.length === 1 ? "source" : "sources"} · by name`}
          rows={shown}
          keyOf={(row) => row.definition}
          current={(row) => row.definition === definition}
          pinned={listed}
          loading={catalogue.loading && !catalogue.data}
          error={catalogue.error ?? undefined}
          onRetry={catalogue.refresh}
          empty="No source matches."
          foot={cutWords(shown.length, matching.length)}
          row={(row, on) => (
            <RailRow
              key={row.definition}
              item={row.definition}
              name={row.name}
              // Named by both lines, so the name holds every word the row shows (WCAG 2.5.3).
              label={`${row.name}, ${definitionLine(row)}`}
              current={on}
              href={definitionHref(base, row.definition, filter)}
              mark={<ProductLogo size="sm" fallback={Rss} />}
            >
              <RailName current={on}>{row.name}</RailName>
              {/* Two block lines read as words apart, not "01Public", by a checker of the name. */}{" "}
              <RailLine>{definitionLine(row)}</RailLine>
            </RailRow>
          )}
        />
      </div>
    )
  }

  return (
    <>
      <p className="text-hint text-muted-foreground">
        <BackLink href={`${base}/stack`}>Stack</BackLink>
      </p>
      <CurrentSources
        base={base}
        enabled={named}
        canRemove={can("destructive") && !project.archived}
      />
      <Panel plain data-slot="source-catalogue">
        <PanelHeader
          title="Add a source"
          count={catalogue.data ? matching.length.toLocaleString("en") : undefined}
        />
        <PanelBody className="space-y-3 pt-3">
          <p className="max-w-3xl text-body leading-relaxed text-muted-foreground">
            Choose one you are allowed to use. Prowlarr tests it before it is kept, then Radarr and
            Sonarr search it for what is asked for in Seerr.
          </p>
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Name or description"
              aria-label="Search the sources"
              containerClassName="sm:w-72"
            />
            {filters}
          </div>
          <CatalogueList
            base={base}
            filter={filter}
            loading={catalogue.loading && !catalogue.data}
            error={catalogue.error}
            onRetry={catalogue.refresh}
            rows={shown}
            total={matching.length}
          />
        </PanelBody>
      </Panel>
    </>
  )
}

/** The catalogue as rows to open, each with its kind; cut to its first rows, which the search reaches past. */
function CatalogueList({
  base,
  filter,
  loading,
  error,
  onRetry,
  rows,
  total,
}: {
  base: string
  filter: PrivacyFilter
  loading: boolean
  error: unknown
  onRetry: () => void
  rows: SmartStackSourceCatalogue["definitions"]
  total: number
}) {
  if (loading) return <ChoiceListSkeleton rows={6} />
  if (error && rows.length === 0)
    return <SourcesError error={error} onRetry={onRetry} what="the sources Prowlarr can add" />
  if (rows.length === 0) return <EmptyNote>No source matches.</EmptyNote>
  const foot = cutWords(rows.length, total)
  return (
    <>
      <ChoiceList aria-label="Sources to add">
        {rows.map((row) => (
          <ChoiceRow
            key={row.definition}
            verb={`Open ${row.name}`}
            href={definitionHref(base, row.definition, filter)}
            leading={<ProductLogo size="sm" fallback={Rss} />}
            title={row.name}
            description={row.description || definitionLine(row)}
            trailing={
              <span className="flex items-center gap-2 max-sm:hidden">
                {row.needsInput && <Tag>Asks for your account</Tag>}
                <Tag tone={row.privacy === "public" ? "success" : "default"}>
                  {PRIVACY_WORD[row.privacy]}
                </Tag>
              </span>
            }
          />
        ))}
      </ChoiceList>
      {foot && <p className="text-hint text-muted-foreground">{foot}</p>}
    </>
  )
}

/** A route's refusal in words (no live release, no Prowlarr), else the error as it is. */
function SourcesError({
  error,
  onRetry,
  what,
}: {
  error: unknown
  onRetry: () => void
  what: string
}) {
  const words = error instanceof ApiError ? sourcesRefusal(error.code) : undefined
  if (words)
    return (
      <Notice
        data-slot="sources-refusal"
        tone={
          error instanceof ApiError && error.code === "stack_app_unreachable"
            ? "warning"
            : "default"
        }
        title="The sources cannot be read now"
      >
        <p>{words}</p>
        {error instanceof ApiError && error.code === "stack_app_unreachable" && (
          <div className="pt-1">
            <Button size="sm" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          </div>
        )}
      </Notice>
    )
  return (
    <ErrorState
      error={error instanceof Error ? error : new Error(String(error))}
      onRetry={onRetry}
      what={what}
    />
  )
}

/**
 * The sources the stack has, each one row of one height: its name, its kind,
 * and Remove behind its menu, confirmed, since Radarr and Sonarr stop
 * searching it at once.
 */
function CurrentSources({
  base,
  enabled,
  canRemove,
}: {
  base: string
  enabled: boolean
  canRemove: boolean
}) {
  const sources = usePoll(
    (signal) => get<{ sources: SmartStackSource[] }>(`${base}/stack/sources`, undefined, signal),
    15_000,
    [base],
    { enabled, key: `${base}/stack/sources` },
  )
  const { confirm, dialog } = useConfirm()
  const list = sources.data?.sources ?? []
  const remove = (source: SmartStackSource) =>
    confirm({
      title: "Remove source",
      description: `${source.name} is removed from Prowlarr, and Radarr and Sonarr stop searching it. What it already found stays in the library.`,
      subject: {
        mark: <ProductLogo size="sm" fallback={Rss} />,
        name: source.name,
        facts: <FormFact label="Kind">{PRIVACY_WORD[source.privacy]}</FormFact>,
      },
      confirmLabel: "Remove",
      action: async () => {
        await del(`${base}/stack/sources/${source.id}`)
      },
      onDone: sources.refresh,
    })

  return (
    <Panel plain data-slot="stack-sources">
      <PanelHeader title="Media sources" count={sources.data ? list.length : undefined} />
      <PanelBody className="pt-3">
        {sources.loading && !sources.data ? (
          <ChoiceListSkeleton rows={2} />
        ) : sources.error && !sources.data ? (
          <SourcesError
            error={sources.error}
            onRetry={sources.refresh}
            what="the stack's sources"
          />
        ) : list.length === 0 ? (
          <p className="max-w-3xl text-body leading-relaxed text-muted-foreground">
            None yet. Radarr and Sonarr cannot fetch anything until the stack has one: choose one
            below.
          </p>
        ) : (
          <ChoiceList aria-label="Media sources">
            {list.map((source) => (
              <ChoiceRow
                key={source.id}
                disabled
                arrow={false}
                verb={source.name}
                leading={<ProductLogo size="sm" fallback={Rss} />}
                title={source.name}
                description={[PRIVACY_WORD[source.privacy], source.protocol]
                  .filter(Boolean)
                  .join(" · ")}
                trailing={
                  <Tag tone={source.enabled ? "success" : "default"}>
                    {source.enabled ? "In use" : "Turned off"}
                  </Tag>
                }
                actions={
                  canRemove ? (
                    <DimActions>
                      <VerbMenu
                        label={`More for ${source.name}`}
                        verbs={[
                          {
                            key: "remove",
                            label: "Remove",
                            icon: Trash,
                            danger: true,
                            run: () => remove(source),
                          },
                        ]}
                      />
                    </DimActions>
                  ) : undefined
                }
              />
            ))}
          </ChoiceList>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

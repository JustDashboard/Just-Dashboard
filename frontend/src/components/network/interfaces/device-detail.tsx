"use client"

import { get } from "@/lib/api"
import type { NetworkLinkDetail, NetworkReading } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { errorRows } from "./device-reading"

/**
 * What the list leaves out because it costs a process per device: the driver
 * behind it, the work its NIC does for the kernel, and every error counter
 * the kernel keeps. Read when a view that shows it opens and again every half
 * minute; a part that could not be read says why instead of reading as empty.
 */
export function useDeviceDetail(name: string) {
  return usePoll<NetworkLinkDetail>(
    (signal) => get(`/network/links/${encodeURIComponent(name)}/detail`, undefined, signal),
    30_000,
    [name],
  )
}

type DetailRead = ReturnType<typeof useDeviceDetail>

/** The reading's own states, around whatever a section draws from it. */
function DetailBody({
  detail,
  rows,
  children,
}: {
  detail: DetailRead
  /** How many lines the section holds while it reads. */
  rows: number
  children: (d: NetworkLinkDetail) => React.ReactNode
}) {
  const d = detail.data
  return (
    <>
      {d && (
        <NetworkReadWarning
          error={detail.error}
          refresh={detail.refresh}
          lastSuccess={detail.lastSuccess}
          reading="device detail"
        />
      )}
      {!d ? (
        detail.error ? (
          <Notice tone="warning" title="The device detail could not be read">
            {detail.error.message}
          </Notice>
        ) : (
          <LoadingRows rows={rows} />
        )
      ) : (
        children(d)
      )}
    </>
  )
}

/** Every error counter the kernel keeps for the link, the ones that counted anything. */
export function DeviceErrors({ detail }: { detail: DetailRead }) {
  return (
    <Panel plain>
      <PanelHeader title="Link errors" />
      <PanelBody>
        <DetailBody detail={detail} rows={2}>
          {(d) =>
            d.errorsRead.state !== "ok" ? (
              <ReadingWords reading={d.errorsRead} />
            ) : errorRows(d.errors).length === 0 ? (
              <p className="text-body text-muted-foreground">
                No CRC, frame, FIFO, carrier or drop has been counted.
              </p>
            ) : (
              <DetailList aria-label="Link errors">
                {errorRows(d.errors).map((row) => (
                  <Detail key={row.label} label={row.label}>
                    <span className="numeric text-warning">{row.value.toLocaleString()}</span>
                  </Detail>
                ))}
              </DetailList>
            )
          }
        </DetailBody>
      </PanelBody>
    </Panel>
  )
}

/** The driver behind the device and the work its NIC takes off the kernel, as a settings section. */
export function DeviceDriver({ detail }: { detail: DetailRead }) {
  return (
    <SettingSection title="Driver">
      <DetailBody detail={detail} rows={3}>
        {(d) => (
          <div className="flex flex-col gap-4">
            <DetailList>
              <Detail label="Driver">
                {d.driver ? (
                  <span className="font-mono">
                    {d.driver.name}
                    {d.driver.version ? ` ${d.driver.version}` : ""}
                  </span>
                ) : (
                  <ReadingWords reading={d.driverRead} />
                )}
              </Detail>
              {d.driver?.firmware && <Detail label="Firmware">{d.driver.firmware}</Detail>}
              {d.driver?.bus && (
                <Detail label="Bus">
                  <span className="font-mono">{d.driver.bus}</span>
                </Detail>
              )}
            </DetailList>
            <div>
              <p className="eyebrow mb-2">Offloads</p>
              {d.offloadsRead.state !== "ok" ? (
                <ReadingWords reading={d.offloadsRead} />
              ) : (
                <ul className="flex flex-wrap gap-1.5" aria-label="Offloads">
                  {d.offloads.map((o) => (
                    <li key={o.name}>
                      <Tag className={o.enabled ? undefined : "text-muted-foreground line-through"}>
                        {o.name}
                        {o.fixed ? " · fixed" : ""}
                      </Tag>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        )}
      </DetailBody>
    </SettingSection>
  )
}

/** A part of a reading that did not arrive, in words. */
export function ReadingWords({ reading }: { reading: NetworkReading }) {
  const words =
    reading.state === "unavailable"
      ? `Not available on this host${reading.package ? ` (install ${reading.package})` : ""}.`
      : reading.state === "not_applicable"
        ? (reading.reason ?? "Not applicable to this device.")
        : `Could not be read${reading.reason ? `: ${reading.reason}` : "."}`
  return (
    <span
      className={
        reading.state === "failed" ? "text-body text-warning" : "text-body text-muted-foreground"
      }
    >
      {words}
    </span>
  )
}

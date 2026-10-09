"use client"

import { get } from "@/lib/api"
import type { NetworkLinkDetail, NetworkReading } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { errorRows } from "./device-reading"

/**
 * What the list leaves out because it costs a process per device: the driver
 * behind it, the work its NIC does for the kernel, and every error counter
 * the kernel keeps. Read when the sheet opens and again every half minute;
 * a part that could not be read says why instead of reading as empty.
 */
export function DeviceHardware({ name, open }: { name: string; open: boolean }) {
  const detail = usePoll<NetworkLinkDetail>(
    (signal) => get(`/network/links/${encodeURIComponent(name)}/detail`, undefined, signal),
    30_000,
    [name],
    { enabled: open },
  )
  const d = detail.data
  return (
    <Panel plain>
      <PanelHeader title="Driver and errors" />
      <PanelBody>
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
            <p className="text-body text-muted-foreground">Reading…</p>
          )
        ) : (
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

            <div>
              <p className="eyebrow mb-2">Link errors since it was made</p>
              {d.errorsRead.state !== "ok" ? (
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
              )}
            </div>
          </div>
        )}
      </PanelBody>
    </Panel>
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

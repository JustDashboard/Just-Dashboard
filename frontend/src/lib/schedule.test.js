import { describe, expect, test } from "bun:test"
import {
  axisHours,
  countdown,
  cronProgram,
  cronRunsBetween,
  describeCalendar,
  execCommand,
  randomDelay,
  RUN_CAP,
  scheduleLanes,
  timerTriggers,
} from "./schedule"

const H = 3600_000

// A cron line names the program it runs after its guards and its
// assignments, and that program is its lane's name and its mark.
describe("cronProgram", () => {
  test("reads past cd, test and command -v to the work", () => {
    expect(cronProgram("cd / && run-parts --report /etc/cron.hourly").name).toBe("run-parts")
    expect(
      cronProgram("test -x /usr/sbin/anacron || run-parts --report /etc/cron.daily").name,
    ).toBe("run-parts")
    expect(cronProgram("command -v debian-sa1 > /dev/null && debian-sa1 1 1").name).toBe(
      "debian-sa1",
    )
  })

  test("skips assignments and redirections, keeps the pipeline's first program", () => {
    expect(cronProgram("SERVICE_MODE=1 /sbin/e2scrub_all -A -r")).toEqual({
      name: "e2scrub_all",
      segment: "/sbin/e2scrub_all -A -r",
    })
    expect(cronProgram("/usr/local/bin/backup >> /var/log/backup.log 2>&1").name).toBe("backup")
    expect(cronProgram("pg_dump -Fc app | gzip > /srv/app.gz").segment).toBe("pg_dump -Fc app")
    expect(cronProgram("docker system prune -af").segment).toBe("docker system prune -af")
  })
})

describe("cronRunsBetween", () => {
  const from = new Date(2026, 9, 7, 0, 10).getTime()

  test("lists each run in the window, soonest first", () => {
    const { times, dense } = cronRunsBetween("0 */6 * * *", from, from + 24 * H)
    expect(dense).toBe(false)
    expect(times.map((t) => new Date(t).getHours())).toEqual([6, 12, 18, 0])
  })

  // Every minute is 1,440 marks a day, which is a band and not a list.
  test("caps a schedule that fires more often than the cap and says so", () => {
    const { times, dense } = cronRunsBetween("* * * * *", from, from + 24 * H)
    expect(dense).toBe(true)
    expect(times).toHaveLength(RUN_CAP)
  })
})

describe("scheduleLanes", () => {
  const from = new Date(2026, 9, 7, 0, 10).getTime()
  const until = from + 24 * H
  const job = (line, schedule, command, extra = {}) => ({
    line,
    schedule,
    command,
    raw: `${schedule} ${command}`,
    disabled: false,
    ...extra,
  })

  const lanes = scheduleLanes({
    crontab: {
      user: "root",
      source: "",
      raw: "",
      env: [],
      comments: [],
      jobs: [
        job(1, "0 3 * * *", "/usr/local/bin/backup", { comment: "nightly backup" }),
        job(2, "0 4 * * *", "/usr/local/bin/prune", { disabled: true }),
        job(3, "@reboot", "pm2 resurrect"),
        job(4, "0 4 * * 0", "docker system prune -af"),
      ],
    },
    timers: [
      {
        unit: "certbot.timer",
        activates: "certbot.service",
        activeState: "active",
        subState: "waiting",
        unitFileState: "enabled",
        enabled: true,
        next: new Date(from + 2 * H).toISOString(),
      },
      {
        unit: "fstrim.timer",
        activates: "fstrim.service",
        activeState: "inactive",
        subState: "dead",
        unitFileState: "disabled",
        enabled: false,
      },
    ],
    system: [
      {
        user: "",
        source: "/etc/crontab",
        raw: "",
        env: [],
        comments: [],
        jobs: [
          job(1, "17 * * * *", "cd / && run-parts --report /etc/cron.hourly", { user: "root" }),
        ],
      },
    ],
    from,
    until,
  })

  test("sorts every kind together by its next run", () => {
    expect(lanes.map((l) => l.key)).toEqual([
      "system:/etc/crontab:1",
      "timer:certbot.timer",
      "cron:1",
      // 2026-10-07 is a Wednesday: Sunday's prune is past the window but
      // still has a next run, ahead of the three with none.
      "cron:4",
      "cron:2",
      "cron:3",
      "timer:fstrim.timer",
    ])
  })

  test("names a lane by its note, else its program, and a timer by its unit", () => {
    const byKey = Object.fromEntries(lanes.map((l) => [l.key, l]))
    expect(byKey["cron:1"].name).toBe("nightly backup")
    expect(byKey["system:/etc/crontab:1"].name).toBe("run-parts")
    expect(byKey["system:/etc/crontab:1"].times).toHaveLength(24)
    expect(byKey["timer:certbot.timer"].name).toBe("certbot")
    expect(byKey["timer:certbot.timer"].times).toEqual([from + 2 * H])
    expect(byKey["cron:4"].times).toEqual([])
    expect(byKey["cron:4"].next).toBeGreaterThan(until)
    expect(byKey["cron:2"].next).toBeUndefined()
    expect(byKey["cron:3"].next).toBeUndefined()
  })
})

test("axisHours marks every step hours on the hour", () => {
  const from = new Date(2026, 9, 7, 0, 10).getTime()
  const hours = axisHours(from, from + 24 * H, 3).map((t) => new Date(t).getHours())
  expect(hours).toEqual([3, 6, 9, 12, 15, 18, 21, 0])
})

test("countdown is to the second under an hour", () => {
  expect(countdown(42_000)).toBe("42s")
  expect(countdown(4 * 60_000 + 7_000)).toBe("4m 07s")
  expect(countdown(2 * H + 5 * 60_000)).toBe("2h 05m")
  expect(countdown(3 * 24 * H + 4 * H)).toBe("3d 4h")
})

describe("describeCalendar", () => {
  test("says the timers the packages ship in words", () => {
    expect(describeCalendar("*-*-* 00,12:00:00")).toBe("twice a day, at 00:00 and 12:00")
    expect(describeCalendar("*-*-* 06,18:00:00")).toBe("twice a day, at 06:00 and 18:00")
    expect(describeCalendar("*-*-* 00:00:00")).toBe("every day at 00:00")
    expect(describeCalendar("Mon *-*-* 00:00:00")).toBe("on Mondays at 00:00")
    expect(describeCalendar("Mon..Fri *-*-* 09:30:00")).toBe("on weekdays at 09:30")
    expect(describeCalendar("*-*-01 03:00:00")).toBe("on day 1 of every month at 03:00")
    expect(describeCalendar("*-*-* *:00/30:00")).toBe("every 30 minutes")
    expect(describeCalendar("*-*-* *:09:00")).toBe("every hour at :09")
  })

  test("keeps the expression for what it cannot say", () => {
    expect(describeCalendar("Sun *-*-1..7 3:10:00")).toBe("Sun *-*-1..7 3:10:00")
    expect(describeCalendar("*-01,07-01 00:00:00")).toBe("*-01,07-01 00:00:00")
  })
})

test("timerTriggers reads the calendar and the monotonic triggers", () => {
  expect(
    timerTriggers({
      TimersCalendar: "{ OnCalendar=*-*-* 00,12:00:00 ; next_elapse=Wed 2026-10-07 12:00:00 UTC }",
    }),
  ).toEqual([{ spec: "*-*-* 00,12:00:00", words: "twice a day, at 00:00 and 12:00" }])
  expect(
    timerTriggers({
      TimersMonotonic:
        "{ OnBootSec=15min ; next_elapse=0 } { OnUnitActiveSec=1d ; next_elapse=1d 2h }",
    }),
  ).toEqual([
    { spec: "OnBootSec=15min", words: "15min after boot" },
    { spec: "OnUnitActiveSec=1d", words: "1d after it last started" },
  ])
  expect(randomDelay({ RandomizedDelayUSec: "12h" })).toBe("12h")
  expect(randomDelay({ RandomizedDelayUSec: "0" })).toBeUndefined()
  expect(
    execCommand({
      ExecStart:
        "{ path=/usr/bin/certbot ; argv[]=/usr/bin/certbot -q renew ; ignore_errors=no ; start_time=[n/a] }",
    }),
  ).toBe("/usr/bin/certbot -q renew")
})

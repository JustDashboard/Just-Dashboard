import { describe, expect, test } from "bun:test"
import {
  calendarToCron,
  clockLabel,
  countdown,
  cronLineName,
  cronProgram,
  cronRunsBetween,
  dayHours,
  deferredToSystemd,
  describeCalendar,
  describeTimer,
  execCommand,
  isExampleLine,
  randomDelay,
  RUN_CAP,
  scheduleLanes,
  spanMs,
  spanWords,
  timerFor,
  timerRuns,
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

  test("uses the job's zone when browser midnight differs", () => {
    const start = Date.parse("2026-10-14T00:00:00Z")
    const { times } = cronRunsBetween(
      "0 3 * * *",
      start,
      start + 24 * H,
      RUN_CAP,
      "America/New_York",
    )
    expect(times).toEqual([Date.parse("2026-10-14T07:00:00Z")])
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
    expect(byKey["system:/etc/crontab:1"].name).toBe("Hourly scripts")
    expect(byKey["system:/etc/crontab:1"].times).toHaveLength(24)
    expect(byKey["timer:certbot.timer"].name).toBe("certbot")
    expect(byKey["timer:certbot.timer"].times).toEqual([from + 2 * H])
    expect(byKey["cron:4"].times).toEqual([])
    expect(byKey["cron:4"].next).toBeGreaterThan(until)
    expect(byKey["cron:2"].next).toBeUndefined()
    expect(byKey["cron:3"].next).toBeUndefined()
  })
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
    { spec: "OnBootSec=15min", words: "15 minutes after boot" },
    { spec: "OnUnitActiveSec=1d", words: "1 day after it last started" },
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

// Debian's /etc/crontab groups its daily line in braces; the band once named
// that lane "}".
test("cronProgram reads through a braced group", () => {
  const daily = "test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }"
  expect(cronProgram(daily)).toEqual({
    name: "run-parts",
    segment: "run-parts --report /etc/cron.daily",
  })
  expect(cronLineName({ command: daily })).toBe("Daily scripts")
})

test("cronProgram keeps arguments that look like assignments and drops redirections", () => {
  expect(cronProgram("curl -fsS https://status.example.com/ping > /dev/null").segment).toBe(
    "curl -fsS https://status.example.com/ping",
  )
  expect(cronProgram("docker system prune -af --filter until=168h").segment).toBe(
    "docker system prune -af --filter until=168h",
  )
  expect(cronProgram("/opt/run.sh 2> /tmp/err < /dev/null").segment).toBe("/opt/run.sh")
})

describe("cronLineName", () => {
  test("names a line without a note by its program and subcommand", () => {
    expect(cronLineName({ command: "docker system prune -af --filter until=168h" })).toBe(
      "docker system prune",
    )
    expect(cronLineName({ command: "@reboot pm2 resurrect".slice(8) })).toBe("pm2 resurrect")
    expect(cronLineName({ command: "/usr/local/bin/backup --to s3" })).toBe("backup")
    expect(cronLineName({ command: "debian-sa1 1 1" })).toBe("debian-sa1")
  })

  test("takes a note that reads as a name, and the program otherwise", () => {
    expect(
      cronLineName({
        command: "command -v debian-sa1 > /dev/null && debian-sa1 1 1",
        comment: "Activity reports every 10 minutes everyday",
      }),
    ).toBe("Activity reports every 10 minutes everyday")
    // The tail of a paragraph is no name.
    expect(
      cronLineName({
        command: "test -x /usr/bin/certbot && certbot -q renew",
        comment: "certbot.timer.",
      }),
    ).toBe("certbot")
    expect(cronLineName({ command: "cd / && run-parts --report /etc/cron.hourly" })).toBe(
      "Hourly scripts",
    )
  })
})

test("isExampleLine knows /etc/crontab's worked example", () => {
  expect(
    isExampleLine({ disabled: true, user: "user-name", command: "command to be executed" }),
  ).toBe(true)
  expect(isExampleLine({ disabled: true, user: "root", command: "/usr/local/bin/prune" })).toBe(
    false,
  )
})

describe("deferredToSystemd", () => {
  test("finds the guard Debian puts on the lines a timer also runs", () => {
    expect(
      deferredToSystemd("test -e /run/systemd/system || SERVICE_MODE=1 /sbin/e2scrub_all -A -r"),
    ).toBe(true)
    expect(
      deferredToSystemd(
        "test -x /usr/bin/certbot -a \\! -d /run/systemd/system && perl -e 'sleep int(rand(43200))' && certbot -q renew",
      ),
    ).toBe(true)
  })

  test("leaves every other line alone", () => {
    expect(deferredToSystemd("command -v debian-sa1 > /dev/null && debian-sa1 1 1")).toBe(false)
    expect(deferredToSystemd("/usr/local/bin/check /run/systemd/system")).toBe(false)
  })

  test("names the timer that does the work", () => {
    const timers = [{ unit: "certbot.timer" }, { unit: "e2scrub_all.timer" }]
    expect(timerFor("test -x /usr/bin/certbot && certbot -q renew", timers)).toBe("certbot.timer")
    expect(
      timerFor("test -e /run/systemd/system || /usr/libexec/e2fsprogs/e2scrub_all_cron", timers),
    ).toBe("e2scrub_all.timer")
    expect(timerFor("/usr/local/bin/backup", timers)).toBeUndefined()
  })
})

test("calendarToCron writes the timers' calendars as cron expressions", () => {
  expect(calendarToCron("*-*-* 06,18:00:00")).toBe("0 6,18 * * *")
  expect(calendarToCron("*-*-* *:00/10:00")).toBe("0-59/10 * * * *")
  expect(calendarToCron("Sun *-*-* 03:10:00")).toBe("10 3 * * 0")
  expect(calendarToCron("Mon..Fri *-*-* 09:30:00")).toBe("30 9 * * 1-5")
  expect(calendarToCron("Sat..Sun *-*-* 09:30:00")).toBe("30 9 * * 6-6,0-0")
  expect(calendarToCron("daily")).toBe("0 0 * * *")
  expect(calendarToCron("*:00/10")).toBe("0-59/10 * * * *")
  expect(calendarToCron("Sun 03:10")).toBe("10 3 * * 0")
  expect(calendarToCron("*-*-01 03:00:00")).toBe("0 3 1 * *")
  expect(calendarToCron("*-01,07-01 00:00:00")).toBeUndefined()
})

test("spans read as systemd writes them", () => {
  expect(spanMs("12h")).toBe(12 * H)
  expect(spanMs("1h 40min")).toBe(H + 40 * 60_000)
  expect(spanMs("whenever")).toBeUndefined()
  expect(spanWords("12h")).toBe("12 hours")
  expect(spanWords("1h 40min")).toBe("1 hour 40 minutes")
  expect(spanWords("1d", true)).toBe("day")
})

describe("describeTimer", () => {
  test("says the calendar, the intervals and the random delay in one sentence", () => {
    expect(describeTimer({ calendar: ["*-*-* 06,18:00:00"], randomDelay: "12h" })).toBe(
      "Twice a day, at 06:00 and 18:00, up to 12 hours later",
    )
    expect(describeTimer({ monotonic: ["OnBootSec=15min", "OnUnitActiveSec=1d"] })).toBe(
      "15 minutes after boot, then every day",
    )
    expect(describeTimer({ calendar: ["*-*-* *:00/10:00"] })).toBe("Every 10 minutes")
    expect(describeTimer({})).toBeUndefined()
  })
})

describe("timerRuns", () => {
  const from = Date.parse("2026-11-09T10:05:00Z")
  const until = from + 24 * H
  const timer = (extra) => ({
    unit: "x.timer",
    activates: "x.service",
    activeState: "active",
    subState: "waiting",
    unitFileState: "enabled",
    enabled: true,
    ...extra,
  })

  test("walks a calendar with no random delay", () => {
    const runs = timerRuns(
      timer({ next: "2026-11-09T10:10:00Z", calendar: ["*-*-* *:00/10:00"] }),
      from,
      until,
      "UTC",
    )
    expect(runs.times).toHaveLength(144)
    expect(runs.dense).toBe(false)
    expect(runs.times[0]).toBe(Date.parse("2026-11-09T10:10:00Z"))
    expect(
      timerRuns(
        timer({ next: "2026-11-09T10:10:00Z", calendar: ["*-*-* *:00/10:00"] }),
        from,
        until,
        "UTC",
        RUN_CAP,
      ).dense,
    ).toBe(true)
  })

  test("says only the next run of a timer with a random delay", () => {
    const runs = timerRuns(
      timer({ next: "2026-11-09T14:41:00Z", calendar: ["*-*-* 06,18:00:00"], randomDelay: "12h" }),
      from,
      until,
      "UTC",
    )
    expect(runs.times).toEqual([Date.parse("2026-11-09T14:41:00Z")])
  })

  test("repeats an interval from its next run", () => {
    const runs = timerRuns(
      timer({ next: "2026-11-09T12:00:00Z", monotonic: ["OnBootSec=1min", "OnUnitActiveSec=6h"] }),
      from,
      until,
      "UTC",
    )
    expect(runs.times.map((t) => new Date(t).getUTCHours())).toEqual([12, 18, 0, 6])
  })

  test("trusts systemd when the calendar disagrees with it", () => {
    const runs = timerRuns(
      timer({ next: "2026-11-09T11:23:00Z", calendar: ["*-*-* 06,18:00:00"] }),
      from,
      until,
      "UTC",
    )
    expect(runs.times).toEqual([Date.parse("2026-11-09T11:23:00Z")])
  })
})

test("clock labels are the host's wall clock", () => {
  const now = Date.parse("2026-11-09T22:30:00Z")
  expect(clockLabel(Date.parse("2026-11-09T22:40:00Z"), now, "UTC")).toEqual({ time: "22:40" })
  expect(clockLabel(Date.parse("2026-11-10T00:59:00Z"), now, "UTC")).toEqual({
    day: "Tue",
    time: "00:59",
  })
  // The same instant, read in New York, is still Monday.
  expect(clockLabel(Date.parse("2026-11-10T00:59:00Z"), now, "America/New_York")).toEqual({
    time: "19:59",
  })
})

test("dayHours starts at this hour on the host's clock", () => {
  const now = Date.parse("2026-11-09T22:35:00Z")
  const hours = dayHours(now, "UTC")
  expect(hours).toHaveLength(24)
  expect(hours[0]).toEqual({ at: Date.parse("2026-11-09T22:00:00Z"), hour: 22, day: "Mon" })
  expect(hours[2].hour).toBe(0)
  expect(hours[2].day).toBe("Tue")
  // India is half an hour off the hour.
  expect(dayHours(now, "Asia/Kolkata")[0].at).toBe(Date.parse("2026-11-09T22:30:00Z"))
})

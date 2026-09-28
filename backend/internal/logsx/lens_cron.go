package logsx

import "strings"

// The cron lens reads cron's own lines: each job it runs, "(root) CMD
// (command -v debian-sa1 > /dev/null && debian-sa1 1 1)", and the two things
// that go wrong without anyone seeing — output thrown away because no mail
// transport is installed, and a job exiting non-zero. Both of those are
// written by the same forked cron process that wrote the CMD line, under the
// same pid, which is how they get the user and command they are about.
func init() {
	register(&Lens{
		ID:     "cron",
		Events: []string{"run", "output_discarded", "session", "error"},
		Attrs:  []string{"user", "command", "pid"},
		New:    func() Reader { return &cronReader{jobs: map[string]cronJob{}} },
	})
}

// cronJobsCap bounds the pids remembered. A job's follow-up lines come within
// its own run, so forgetting everything past a few hundred loses nothing but
// a line about a job that started hours earlier.
const cronJobsCap = 512

type cronJob struct{ user, command string }

type cronReader struct {
	jobs map[string]cronJob
}

func (r *cronReader) Read(l *Line) {
	m := sysEnvelope(l)
	msg := m.text
	var (
		event string
		job   cronJob
	)
	switch {
	case strings.HasPrefix(msg, "pam_unix(cron:session): session "):
		// auth.log and the journal carry the PAM session around every job;
		// it is the one cron line there is more of than runs.
		rest := msg[len("pam_unix(cron:session): session "):]
		if !strings.HasPrefix(rest, "opened for user ") && !strings.HasPrefix(rest, "closed for user ") {
			return
		}
		event, job.user = "session", sysUntil(rest[len("opened for user "):], '(')
	case strings.HasPrefix(msg, "(CRON) "):
		word, rest, _ := strings.Cut(msg[len("(CRON) "):], " ")
		switch {
		case strings.EqualFold(word, "info") && strings.HasPrefix(rest, "(No MTA installed, discarding output)"):
			event = "output_discarded"
		case strings.EqualFold(word, "error"):
			event = "error"
		default:
			return
		}
		job = r.jobs[m.pid]
	case strings.HasPrefix(msg, "("):
		user, rest, ok := strings.Cut(msg[1:], ") ")
		if !ok || !strings.HasPrefix(rest, "CMD (") {
			return
		}
		event, job = "run", cronJob{user: user, command: strings.TrimSuffix(rest[len("CMD ("):], ")")}
		if m.pid != "" {
			if len(r.jobs) >= cronJobsCap {
				clear(r.jobs)
			}
			// Copied, because the line they are cut from is not kept.
			r.jobs[strings.Clone(m.pid)] = cronJob{user: strings.Clone(job.user), command: strings.Clone(job.command)}
		}
	default:
		return
	}
	l.Event = event
	l.SetAttr("user", job.user)
	l.SetAttr("command", job.command)
	if m.own {
		l.SetAttr("pid", m.pid)
	}
	switch event {
	case "output_discarded":
		// What the job printed — usually its error — is gone.
		l.SetLevel("warn")
	case "session":
		l.SetLevel("debug")
	case "error":
		l.SetLevel("error")
	}
}

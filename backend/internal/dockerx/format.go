package dockerx

import (
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

type containerInspect = container.InspectResponse
type healthFacts struct {
	hasCheck bool
	status   string
}

func healthFactsOf(insp containerInspect) healthFacts {
	f := healthFacts{}
	if insp.Config != nil && insp.Config.Healthcheck != nil &&
		len(insp.Config.Healthcheck.Test) > 0 && insp.Config.Healthcheck.Test[0] != "NONE" {
		f.hasCheck = true
	}
	if insp.State != nil && insp.State.Health != nil {
		f.hasCheck = true
		f.status = insp.State.Health.Status
	}
	return f
}

func parseDockerTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sB", float64(b)/float64(div), []string{"K", "M", "G", "T", "P"}[exp])
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

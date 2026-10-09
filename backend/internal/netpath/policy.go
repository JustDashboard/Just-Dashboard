package netpath

import (
	"context"
	"slices"
	"strings"
)

// sitePolicyFact is a proxy site's configured application controls in one
// line, with what the running build lacks: the service policy a request
// through the site meets. Empty when the owner cannot say.
func sitePolicyFact(ctx context.Context, p Providers, site string) string {
	if p.Policy == nil {
		return ""
	}
	policy, err := p.Policy(ctx, site)
	if err != nil || policy == nil {
		return ""
	}
	var parts []string
	for _, c := range policy.Controls {
		if !c.Configured {
			continue
		}
		part := strings.ToLower(c.Title)
		if c.Setting != "" {
			part += " " + c.Setting
		}
		if c.Support == "missing" {
			part += " (this nginx lacks its module)"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "no request limit, cache or HTTP version control is set"
	}
	return strings.Join(parts, " · ")
}

func appendOnce(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

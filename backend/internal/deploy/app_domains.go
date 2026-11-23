package deploy

import (
	"context"
	"strings"
)

// Names under a just-dashboard.com address this server no longer holds.
//
// A deployment that took <app>.<handle>.just-dashboard.com keeps it in its
// configuration after the handle is released, suspended, detached by an
// unlink, or renamed. The site has deleted the records by then, so the name
// resolves nowhere, and no certificate may be ordered for it: names under
// the zone are only ever covered by the handle's wildcard. So every step reads
// the release without such names (decodeReleaseSnapshot), and the certificate
// step says what it left out: a warning naming them when the release has
// other public names, a failure when it has none.

// AppDomainNames places a hostname against the app domain this server holds
// (backend/internal/appdomain).
type AppDomainNames interface {
	// DetachedAppDomain reports a name under the app domain zone that the
	// handle this server holds now does not cover.
	DetachedAppDomain(ctx context.Context, hostname string) bool
}

// WithAppDomains attaches the app domain's view of names. Without it no name
// is ever left out.
func (e *NormalizedStepExecutor) WithAppDomains(names AppDomainNames) *NormalizedStepExecutor {
	e.appDomains = names
	return e
}

// detachedDomains splits domains into those the route may serve and the app
// domain names it may not.
func (e *NormalizedStepExecutor) detachedDomains(ctx context.Context, domains []PlannedDomain) (kept []PlannedDomain, detached []string) {
	if e.appDomains == nil || len(domains) == 0 {
		return domains, nil
	}
	kept = make([]PlannedDomain, 0, len(domains))
	gone := map[string]bool{}
	for _, domain := range domains {
		if e.appDomains.DetachedAppDomain(ctx, domain.Hostname) {
			detached = append(detached, strings.ToLower(domain.Hostname))
			gone[strings.ToLower(domain.Hostname)] = true
			continue
		}
		kept = append(kept, domain)
	}
	if len(detached) == 0 {
		return domains, nil
	}
	// A redirect to a name that is gone would send visitors nowhere.
	out := kept[:0]
	for _, domain := range kept {
		if domain.Redirects() && gone[strings.ToLower(domain.RedirectTo)] {
			detached = append(detached, strings.ToLower(domain.Hostname))
			continue
		}
		out = append(out, domain)
	}
	return out, detached
}

// appDomainDetachedResult is the certificate step's answer for a release
// whose only public names are under a handle this server no longer holds.
func appDomainDetachedResult(detached []string) StepResult {
	return StepResult{
		State: StepFailed, ErrorCode: "app_domain_detached",
		ErrorMessage: detachedSentence(detached) + " This release has no other public name. Claim the address again under Settings → just-dashboard.com link, or give the deployment another address under Settings → Domains, then deploy again.",
		Evidence:     mustJSON(map[string]any{"detached": detached, "outcome": "failed"}),
	}
}

func detachedSentence(detached []string) string {
	verb := "is"
	if len(detached) > 1 {
		verb = "are"
	}
	return strings.Join(detached, ", ") + " " + verb + " under a just-dashboard.com address this server no longer holds, so it is left out of the route."
}

package deploy

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// RouteOwners names the deployment environment that writes each route in a
// list (VHost.Owner), and marks one whose environment or project is
// archived. A route whose environment is gone keeps no owner. The API layer
// answers it from the store, as the Sites list does, so preflight, the
// release and the proxy pages agree on which routes are leftovers.
type RouteOwners interface {
	MarkDeploymentRoutes(context.Context, []proxysvc.VHost) error
}

// DomainClaim is a proxy site that already answers a planned hostname.
type DomainClaim struct {
	Site string `json:"site"`
	// Project and ProjectID are the live deployment that writes the route.
	Project   string `json:"project,omitempty"`
	ProjectID int64  `json:"projectId,omitempty"`
	// Leftover is a deployment's route nothing deploys any more: its project
	// was deleted, or it or its environment archived. Deleting a project
	// keeps what it made on the server, its route included, so the route
	// went on holding the domain and every later deployment of that domain
	// was refused as a conflict with nothing to say why.
	Leftover bool `json:"leftover,omitempty"`
}

var leftoverRouteRe = regexp.MustCompile(`^just-dashboard-env-([1-9][0-9]{0,17})\.conf$`)

// routeEnvironment is the environment whose route a site name is, told by the
// spelling deploymentRouteName gives it and nothing looser.
func routeEnvironment(name string) (int64, bool) {
	m := leftoverRouteRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || deploymentRouteName(id) != name {
		return 0, false
	}
	return id, true
}

// claimOf is what a site that answers a planned name is to the deployment.
// marked says whether the owners were read: without them no route is taken
// for a leftover, as one a live deployment writes would be removed under it.
func claimOf(vhost proxysvc.VHost, marked bool) DomainClaim {
	claim := DomainClaim{Site: vhost.Name}
	if _, ok := routeEnvironment(vhost.Name); !ok || !marked {
		return claim
	}
	if vhost.Owner == nil || vhost.Owner.Archived {
		claim.Leftover = true
		return claim
	}
	claim.Project, claim.ProjectID = vhost.Owner.Project, vhost.Owner.ProjectID
	return claim
}

// onlyLeftovers says whether every claim is a leftover route, which the
// release takes the name over from.
func onlyLeftovers(claims []DomainClaim) bool {
	if len(claims) == 0 {
		return false
	}
	for _, claim := range claims {
		if !claim.Leftover {
			return false
		}
	}
	return true
}

// siteLink is the proxy page of the site itself, not the list it is in.
func siteLink(name string) string {
	return "/proxy/sites/" + url.PathEscape(name)
}

func claimSites(claims []DomainClaim) string {
	names := make([]string, 0, len(claims))
	for _, claim := range claims {
		names = append(names, claim.Site)
	}
	return strings.Join(names, ", ")
}

// domainClaimFinding is the finding for a managed domain another site
// already answers. A leftover route is taken over by the release, so it
// passes and says so; anything else is refused with the claimer named and a
// link to it, or to the deployment that writes it.
func domainClaimFinding(planned PlannedDomain, claims []DomainClaim, field string) PreflightFinding {
	if onlyLeftovers(claims) {
		item := finding("domain_takeover", PreflightPass,
			"Domain is taken over from a leftover route", planned.Hostname+" · "+claimSites(claims),
			"A deleted or archived deployment left this route answering the domain. The release removes it just before it writes this deployment's route, and keeps the certificate.",
			"", "proxy", field)
		item.DeepLink = siteLink(claims[0].Site)
		return item
	}
	var claim DomainClaim
	for _, each := range claims {
		if !each.Leftover {
			claim = each
			break
		}
	}
	if claim.Project != "" {
		item := finding("domain_conflict", PreflightBlocked,
			"Domain is already used by another deployment", planned.Hostname+" · "+claim.Project,
			claim.Project+" serves this domain, and a second route for the same name would make routing ambiguous.",
			"Remove the domain from "+claim.Project+" first, or choose another domain.", "proxy", field)
		item.DeepLink = fmt.Sprintf("/deploy/%d/settings/domains", claim.ProjectID)
		return item
	}
	measured, link := planned.Hostname, "/proxy/sites"
	if claim.Site != "" {
		measured, link = planned.Hostname+" · "+claim.Site, siteLink(claim.Site)
	}
	item := finding("domain_conflict", PreflightBlocked,
		"Domain is already claimed by another proxy site", measured,
		"Creating another managed route would make ownership and routing ambiguous.",
		"Remove or rename that site, or choose another domain.", "proxy", field)
	item.DeepLink = link
	return item
}

// leftoverRouteProxy is what taking a leftover route over needs of the proxy
// beyond activation's own calls.
type leftoverRouteProxy interface {
	ListVHosts(context.Context) ([]proxysvc.VHost, error)
	SnapshotDeploymentRoute(context.Context, string) (proxysvc.DeploymentRouteSnapshot, error)
	RemoveDeploymentRoute(context.Context, string) error
}

// takeOverLeftoverRoutes removes the leftover routes that answer any of the
// release's domains, just before its own route is written: two routes for
// one name are refused by Caddy and ignored by nginx, so the new route could
// not be written beside them. Each is snapshotted first, for
// restoreLeftoverRoutes to put back if the cutover fails. The certificates
// stay: the new route serves the same names. Anything it cannot read or
// remove is left as it is, and the cutover then fails as it would have.
func (e *NormalizedStepExecutor) takeOverLeftoverRoutes(ctx context.Context, execution StepExecution, environmentID int64, domains []string) []proxysvc.DeploymentRouteSnapshot {
	proxy, ok := e.proxy.(leftoverRouteProxy)
	if !ok || e.routeOwners == nil || len(domains) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, domain := range domains {
		wanted[strings.ToLower(domain)] = true
	}
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	vhosts, err := proxy.ListVHosts(listCtx)
	if err != nil {
		return nil
	}
	own := deploymentRouteName(environmentID)
	candidates := []proxysvc.VHost{}
	for _, vhost := range vhosts {
		if vhost.Name == own {
			continue
		}
		if _, ok := routeEnvironment(vhost.Name); !ok {
			continue
		}
		for _, name := range vhost.ServerNames {
			if wanted[strings.ToLower(name)] {
				candidates = append(candidates, vhost)
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if err := e.routeOwners.MarkDeploymentRoutes(listCtx, candidates); err != nil {
		_ = stepLog(execution, "stderr", "The routes already answering this deployment's domains could not be matched to their deployments, so none was taken over: "+err.Error())
		return nil
	}
	taken := []proxysvc.DeploymentRouteSnapshot{}
	for _, vhost := range candidates {
		if !claimOf(vhost, true).Leftover {
			continue
		}
		snapshot, err := proxy.SnapshotDeploymentRoute(ctx, vhost.Name)
		if err != nil {
			_ = stepLog(execution, "stderr", "The leftover route "+vhost.Name+" could not be read, so it was left in place: "+err.Error())
			continue
		}
		if err := proxy.RemoveDeploymentRoute(ctx, vhost.Name); err != nil {
			_ = stepLog(execution, "stderr", "The leftover route "+vhost.Name+" could not be removed: "+err.Error())
			continue
		}
		taken = append(taken, snapshot)
		_ = stepLog(execution, "status", "Took "+strings.Join(vhost.ServerNames, ", ")+" over from "+vhost.Name+", a route a deleted or archived deployment left")
	}
	return taken
}

// restoreLeftoverRoutes puts back the leftover routes a failed cutover took
// over, so a release that did not go live takes nothing with it.
func (e *NormalizedStepExecutor) restoreLeftoverRoutes(ctx context.Context, execution StepExecution, taken []proxysvc.DeploymentRouteSnapshot) {
	if len(taken) == 0 {
		return
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, snapshot := range taken {
		if err := e.proxy.RestoreDeploymentRoute(recovery, snapshot); err != nil {
			_ = stepLog(execution, "stderr", "The leftover route "+snapshot.Name+" could not be put back: "+err.Error())
			continue
		}
		_ = stepLog(execution, "status", "Put the leftover route "+snapshot.Name+" back")
	}
}

package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// Tailnet-only environments (tailnet-apps.md).
//
// An environment whose plan carries runtime.tailnet is published the way a
// pull request preview is: one port of the host's tailnet address, from the
// previews' own range and table, that tailscale serve maps to loopback. It
// has no public route. tailscale serve maps the port to the environment's
// front, a route on loopback in front of the container, rather than to the
// container itself: the address is a port of the node's MagicDNS name, which
// is the name the dashboard is reached by, so a browser signed in to the
// dashboard sends its session cookie there, and the front takes it out of
// what the application is sent. When the plan names who may open it, the
// front is the gate as well and asks the dashboard about every request; when
// it does not, the tailnet is the fence, exactly as for a preview.

// TailnetGateProxy is the proxy's half of a front. proxysvc.Service is the
// host implementation; a proxy without it cannot gate, and an environment
// that names its visitors fails to activate rather than going live open.
// Without it an environment that does not is published straight to the
// container, as before every tailnet address had a front.
type TailnetGateProxy interface {
	ApplyTailnetGate(context.Context, proxysvc.TailnetGateRoute) (proxysvc.DeploymentRouteResult, error)
	VerifyTailnetGate(context.Context, proxysvc.TailnetGateRoute) error
	RemoveTailnetGate(context.Context, string) error
}

// frontsTailnet says whether this host's proxy can put a front before a
// tailnet address. Every production executor's can; only an executor built
// without the proxy's half publishes an address straight to its container.
func (e *NormalizedStepExecutor) frontsTailnet() bool {
	_, ok := e.proxy.(TailnetGateProxy)
	return ok
}

// WithTailnetGate names the dashboard's own API on loopback,
// http://127.0.0.1:<port>, which is where the gate asks.
func (e *NormalizedStepExecutor) WithTailnetGate(api string) *NormalizedStepExecutor {
	e.tailnetCheck = strings.TrimSuffix(api, "/")
	return e
}

// tailnetGateCheck is the whois route the gate asks for one environment.
func tailnetGateCheck(api string, environmentID int64) string {
	return api + tailnetgate.Path + "?" + tailnetgate.EnvironmentParam + "=" + strconv.FormatInt(environmentID, 10)
}

// tailnetGateEvidence is what activation records about the gate.
type tailnetGateEvidence struct {
	Name     string `json:"name"`
	Listen   int    `json:"listen"`
	Allowed  int    `json:"allowed"`
	Applied  bool   `json:"applied"`
	Verified bool   `json:"verified"`
}

// LiveTailnetAllow is who may open the environment's tailnet address, as its
// live release has it — the list the gate in front of that release was
// written for. Nil is an environment that does not decide by identity: not
// tailnet-only, tailnet-only without a list, or without a live release.
func (s *OrchestrationStore) LiveTailnetAllow(ctx context.Context, environmentID int64) ([]string, error) {
	live, err := s.LiveRelease(ctx, environmentID)
	if errors.Is(err, ErrArtifactMissing) || errors.Is(err, ErrEnvironmentNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(live)
	if err != nil {
		return nil, err
	}
	if !snapshot.Plan.Tailnet.IdentityAware() {
		return nil, nil
	}
	return snapshot.Plan.Tailnet.Allow, nil
}

// tailnetAddressFor is the environment's tailnet address: a preview's, made
// with its approval, or a tailnet-only environment's, made the first time a
// release of it is published. An environment that is neither has none.
func (e *NormalizedStepExecutor) tailnetAddressFor(ctx context.Context, environmentID int64, plan RuntimePlanConfig) (*PreviewAddress, *StepResult) {
	address, err := e.store.PreviewAddressFor(ctx, environmentID)
	if err != nil {
		failure := normalizedStepFailure(err)
		return nil, &failure
	}
	if address != nil || plan.Tailnet == nil || plan.PreviewIsolation {
		return address, nil
	}
	if e.tailnet == nil {
		return nil, &StepResult{State: StepUnavailable, ErrorCode: "tailnet_unavailable",
			ErrorMessage: "this environment is published on the tailnet alone, but Tailscale Serve is unavailable on this host"}
	}
	served, err := e.tailnet.ServedTailnetPorts(ctx)
	if err != nil {
		return nil, &StepResult{State: StepUnavailable, ErrorCode: "tailnet_unavailable",
			ErrorMessage: "the tailnet's serve configuration could not be read: " + err.Error()}
	}
	exclude := make(map[int]int, len(served))
	for port, upstream := range served {
		exclude[port] = upstream
	}
	address, err = e.store.AllocateTailnetAddress(ctx, environmentID, exclude)
	switch {
	case errors.Is(err, ErrPreviewAddressExhausted):
		return nil, &StepResult{State: StepFailed, ErrorCode: "preview_address_exhausted",
			ErrorMessage: fmt.Sprintf("every tailnet port from %d to %d is in use", previewTailnetPortMin, previewTailnetPortMax)}
	case err != nil:
		failure := normalizedStepFailure(err)
		return nil, &failure
	}
	return address, nil
}

func loopbackFree(port int) bool {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// applyTailnetGate writes the environment's front before the candidate: its
// gate, asking the dashboard about every visitor, when the plan names who may
// open it. The route result is returned whether or not it failed, so the
// recovery that follows a failure puts the previous front back.
func (e *NormalizedStepExecutor) applyTailnetGate(
	ctx context.Context,
	execution StepExecution,
	environmentID int64,
	address PreviewAddress,
	runtime ReleaseRuntime,
	plan RuntimePlanConfig,
) (*tailnetGateEvidence, *proxysvc.DeploymentRouteResult, *StepResult) {
	gated := plan.Tailnet.IdentityAware()
	gates, ok := e.proxy.(TailnetGateProxy)
	if !ok || (gated && e.tailnetCheck == "") {
		return nil, nil, &StepResult{State: StepUnavailable, ErrorCode: "tailnet_gate_unavailable",
			ErrorMessage: "this environment admits only the tailnet logins and tags it lists, and no proxy on this host can check them"}
	}
	if runtime.Port == 0 {
		return nil, nil, &StepResult{State: StepFailed, ErrorCode: "preview_port_missing",
			ErrorMessage: "the runtime plan has no internal port to publish"}
	}
	route := proxysvc.TailnetGateRoute{
		Name: deploymentRouteName(environmentID), Listen: address.Port,
		Upstream:  "http://" + net.JoinHostPort(runtimeCheckHost(runtime.Host), strconv.Itoa(runtime.Port)),
		MaxBodyMB: plan.MaxRequestBodyMB,
	}
	evidence := &tailnetGateEvidence{Name: route.Name, Listen: route.Listen}
	what := "front"
	if gated {
		route.Check = tailnetGateCheck(e.tailnetCheck, environmentID)
		evidence.Allowed, what = len(plan.Tailnet.Allow), "gate"
	}
	applied, err := gates.ApplyTailnetGate(ctx, route)
	evidence.Applied = applied.Applied
	if err != nil {
		return evidence, &applied, &StepResult{State: StepFailed, ErrorCode: "tailnet_gate_failed",
			ErrorMessage: "the tailnet " + what + " could not be put in front of the candidate: " + err.Error()}
	}
	if err := gates.VerifyTailnetGate(ctx, route); err != nil {
		return evidence, &applied, &StepResult{State: StepFailed, ErrorCode: "tailnet_gate_failed",
			ErrorMessage: "the tailnet " + what + " could not be verified: " + err.Error()}
	}
	evidence.Verified = true
	if gated {
		_ = stepLog(execution, "status", fmt.Sprintf("The tailnet gate asks who every visitor is and admits %d listed logins and tags", len(plan.Tailnet.Allow)))
	} else {
		_ = stepLog(execution, "status", "The tailnet address reaches the candidate through the dashboard's front, which keeps the dashboard's session cookie from it")
	}
	return evidence, &applied, nil
}

// predecessorFronted says whether the release before a failed candidate was
// reached through the environment's front. route is what activation wrote
// before the candidate, whose snapshot is what stood there until then and
// what the recovery has just put back; without one nothing was written, the
// mapping was never moved, and the address row still says what it points
// at: the front answers on the tailnet port's own number.
func predecessorFronted(address PreviewAddress, route *proxysvc.DeploymentRouteResult) bool {
	if route == nil || route.Snapshot.Name == "" {
		return address.UpstreamPort != 0 && address.UpstreamPort == address.Port
	}
	return route.Snapshot.Existed && proxysvc.IsTailnetGateSnapshot(route.Snapshot)
}

// dropTailnetGate takes away a front the environment no longer needs, once
// what replaced it is published. A failure is logged rather than failing a
// release that is already live: the front left behind answers nobody, since
// tailscale serve no longer points at it.
func (e *NormalizedStepExecutor) dropTailnetGate(ctx context.Context, execution StepExecution, environmentID int64) {
	gates, ok := e.proxy.(TailnetGateProxy)
	if !ok {
		return
	}
	if err := gates.RemoveTailnetGate(context.WithoutCancel(ctx), deploymentRouteName(environmentID)); err != nil {
		_ = stepLog(execution, "stderr", "The tailnet gate this environment no longer needs could not be removed: "+err.Error())
	}
}

// TailnetFronting is what FrontTailnetAddresses put right, for the caller's
// log and audit: the environments whose address now reaches the container
// through its front, and those that could not be moved, with why.
type TailnetFronting struct {
	Fronted []int64
	Failed  map[int64]string
}

// FrontTailnetAddresses puts a front before every tailnet address still
// mapped straight to its container — one published before every address had
// a front — so the dashboard's session cookie stops reaching it at the
// upgrade rather than at the environment's next release. The caller runs it
// after the sweep and before the engine starts, so nothing publishes while a
// mapping moves. The front is written and verified before the mapping moves
// to it, the mapping is recorded before the move counts, and an address
// whose front cannot be written, verified, published or recorded is left as
// it was, its front taken away again: its next release puts one there.
func (e *NormalizedStepExecutor) FrontTailnetAddresses(ctx context.Context) (TailnetFronting, error) {
	out := TailnetFronting{Failed: map[int64]string{}}
	if e == nil || e.store == nil || e.tailnet == nil {
		return out, nil
	}
	gates, ok := e.proxy.(TailnetGateProxy)
	if !ok {
		return out, nil
	}
	addresses, err := e.store.ListPreviewAddresses(ctx)
	if err != nil {
		return out, err
	}
	environments := make([]int64, 0, len(addresses))
	for environmentID, address := range addresses {
		if address.Kind == previewAddressTailnet && address.Published && address.UpstreamPort != 0 && address.UpstreamPort != address.Port {
			environments = append(environments, environmentID)
		}
	}
	slices.Sort(environments)
	for _, environmentID := range environments {
		address := addresses[environmentID]
		if err := e.frontTailnetAddress(ctx, gates, environmentID, address); err != nil {
			out.Failed[environmentID] = err.Error()
			continue
		}
		out.Fronted = append(out.Fronted, environmentID)
	}
	return out, nil
}

func (e *NormalizedStepExecutor) frontTailnetAddress(ctx context.Context, gates TailnetGateProxy, environmentID int64, address PreviewAddress) error {
	live, err := e.store.LiveRelease(ctx, environmentID)
	if err != nil {
		return fmt.Errorf("its live release could not be read: %w", err)
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(live)
	if err != nil {
		return err
	}
	// A gated environment is never mapped past its gate; one that looks it
	// is not this pass's to touch.
	if snapshot.Plan.Tailnet.IdentityAware() {
		return errors.New("it names its visitors, and is published through its gate by its next release")
	}
	route := proxysvc.TailnetGateRoute{
		Name: deploymentRouteName(environmentID), Listen: address.Port,
		Upstream:  "http://127.0.0.1:" + strconv.Itoa(address.UpstreamPort),
		MaxBodyMB: snapshot.Plan.MaxRequestBodyMB,
	}
	applied, err := gates.ApplyTailnetGate(ctx, route)
	if err != nil {
		return fmt.Errorf("its front could not be written: %w", err)
	}
	undo := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if restoreErr := e.proxy.RestoreDeploymentRoute(recovery, applied.Snapshot); restoreErr != nil {
			return fmt.Errorf("%w; the front could not be taken away again: %v", cause, restoreErr)
		}
		return cause
	}
	if err := gates.VerifyTailnetGate(ctx, route); err != nil {
		return undo(fmt.Errorf("its front could not be verified: %w", err))
	}
	url, err := e.tailnet.PublishTailnet(ctx, address.Port, address.Port, address.UpstreamPort)
	if err != nil {
		return undo(fmt.Errorf("its mapping could not be moved to the front: %w", err))
	}
	if err := e.store.MarkPreviewAddressPublished(context.WithoutCancel(ctx), environmentID, url, address.Port, true); err != nil {
		cause := fmt.Errorf("its mapping moved to the front but could not be recorded: %w", err)
		if _, backErr := e.tailnet.PublishTailnet(context.WithoutCancel(ctx), address.Port, address.UpstreamPort, address.Port); backErr != nil {
			return fmt.Errorf("%w; the mapping could not be pointed back at the container either: %v", cause, backErr)
		}
		return undo(cause)
	}
	return nil
}

// leftTheTailnet says the release replaced a tailnet-only one of an
// environment that is not a preview: what makes its address the
// environment's to withdraw rather than a preview's to keep.
func (e *NormalizedStepExecutor) leftTheTailnet(ctx context.Context, release Release) bool {
	if release.PredecessorReleaseID == 0 {
		return false
	}
	var kind EnvironmentKind
	if err := e.store.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, release.EnvironmentID).Scan(&kind); err != nil || kind == EnvironmentPreview {
		return false
	}
	_, previous, err := e.releaseSnapshot(ctx, release.PredecessorReleaseID)
	return err == nil && previous.Plan.Tailnet != nil
}

// leaveTailnet withdraws the address of an environment whose live release
// is no longer tailnet-only, once that release is live: the mapping comes
// down and the port is freed. A failure keeps the row, so the next release
// tries again.
func (e *NormalizedStepExecutor) leaveTailnet(ctx context.Context, execution StepExecution, environmentID int64) {
	ctx = context.WithoutCancel(ctx)
	if _, err := e.withdrawPreviewAddress(ctx, environmentID); err != nil {
		_ = stepLog(execution, "stderr", "The tailnet address this environment no longer uses could not be withdrawn: "+err.Error())
		return
	}
	e.dropTailnetGate(ctx, execution, environmentID)
	_ = stepLog(execution, "status", "Withdrew the tailnet address: this environment is no longer tailnet-only")
}

// tailnetFindings is what preflight says about a tailnet-only plan: the
// address the application will have, over which scheme, and who may open
// it — or that this host is on no tailnet, which no deployment can fix.
func tailnetFindings(configuration PlanConfiguration, observed *TailnetObservation) []PreflightFinding {
	tailnet := configuration.Runtime.Tailnet
	if tailnet == nil {
		return nil
	}
	if observed == nil {
		return []PreflightFinding{finding("tailnet_unobserved", PreflightUnavailable,
			"This host's tailnet could not be read", "",
			"A tailnet-only environment is published by tailscale serve on this host's tailnet address; whether that is possible was not checked.",
			"", "network", "runtime.tailnet")}
	}
	if !observed.Usable {
		return []PreflightFinding{finding("tailnet_unavailable", PreflightBlocked,
			"This host is not on a tailnet", observed.Detail,
			"A tailnet-only environment is reached through tailscale serve on this host's tailnet address, and without a running, signed-in Tailscale nothing could reach it.",
			"Sign this server in to Tailscale (tailscale up on the host), then deploy.", "network", "runtime.tailnet")}
	}
	scheme, carried := "https", "over HTTPS with the tailnet's own certificate, which tailscale serve obtains"
	if !observed.HTTPS {
		scheme, carried = "http", "over plain HTTP inside the tailnet's encrypted tunnel, because HTTPS certificates are off for this tailnet — turn them on in the Tailscale admin console's DNS page and the next deployment serves HTTPS"
	}
	address, held := observed.URL, "It keeps this address from release to release."
	if address == "" {
		port := observed.NextPort
		if port == 0 {
			address = fmt.Sprintf("%s://%s on a port from %d to %d", scheme, observed.Hostname, previewTailnetPortMin, previewTailnetPortMax)
		} else {
			address = fmt.Sprintf("%s://%s:%d", scheme, observed.Hostname, port)
		}
		held = fmt.Sprintf("The port is the lowest free one from %d to %d when it is first deployed, and it is kept from then on.", previewTailnetPortMin, previewTailnetPortMax)
	}
	findings := []PreflightFinding{finding("tailnet_address", PreflightPass,
		"Published on the tailnet alone", address,
		"Only devices on your tailnet reach it, "+carried+". It has no public domain and no public certificate, and nothing is published on a public address. "+held,
		"", "network", "runtime.tailnet")}
	if !tailnet.IdentityAware() {
		return append(findings, finding("tailnet_identity", PreflightPass,
			"Everyone on the tailnet can open it", "",
			"The tailnet is its fence, as it is for a pull request preview. Requests reach it through the dashboard's own front on loopback, which keeps the dashboard's session cookie from it: the address is a port of the name the dashboard is reached by. List Tailscale logins or tags under Domains to admit only them.",
			"", "network", "runtime.tailnet.allow"))
	}
	gate := "this host's nginx"
	switch observed.Gate {
	case "caddy":
		gate = "the dashboard's own Caddy on loopback (just-dashboard-tailnet)"
		if !observed.GateRunning {
			gate += ", which the deployment starts"
		}
	case "":
		gate = "the host's proxy"
	}
	return append(findings, finding("tailnet_identity", PreflightPass,
		"Only the listed logins and tags open it", strings.Join(tailnet.Allow, ", "),
		"Every request first goes to "+gate+", which asks the dashboard who it is: the dashboard asks tailscaled which login or tagged machine holds the address it came from. Anyone else is answered 403, and if tailscaled cannot be asked, 503. The application is told X-Tailnet-Login and X-Tailnet-User, and is never sent the dashboard's session cookie.",
		"", "network", "runtime.tailnet.allow"))
}

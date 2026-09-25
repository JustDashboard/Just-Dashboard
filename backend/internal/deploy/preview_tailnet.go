package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// A preview's tailnet address is one port on this host's Tailscale node
// that `tailscale serve` maps to the preview's loopback publication. The
// range is the dashboard's own: selfcfg's TailnetServe refuses a port
// outside it before any argv exists, and the startup sweep treats a served
// port inside it that no preview owns as a leftover to withdraw.
const (
	previewTailnetPortMin = 21000
	previewTailnetPortMax = 21999
	previewAddressTailnet = "tailnet"
)

var (
	ErrPreviewAddressExhausted = errors.New("every tailnet preview port is in use")
	ErrPreviewAddressMissing   = errors.New("the preview has no tailnet address")
)

// TailnetPublisher is `tailscale serve` as the executor needs it: publish a
// loopback port on the node's tailnet address, take it back, and say which
// ports are served right now. selfcfg's TailnetServe is the host
// implementation; tests use a fake.
type TailnetPublisher interface {
	// PublishTailnet maps the tailnet port to http://127.0.0.1:upstreamPort
	// and returns the URL the operator opens. previousUpstream is the
	// loopback port this dashboard last published on that tailnet port
	// (0 = none), which is how the publisher tells its own earlier mapping
	// from something the operator served by hand and must not replace.
	PublishTailnet(ctx context.Context, port, upstreamPort, previousUpstream int) (url string, err error)
	WithdrawTailnet(ctx context.Context, port int) error
	// ServedTailnetPorts is tailscale's serve config by port. The value is
	// the loopback port a plain, unfunnelled http://127.0.0.1:<port> proxy
	// points at — the one shape of mapping the dashboard makes — and 0 for
	// anything else served there: a directory, a funnel, a raw forwarder.
	// Allocation steps around every key; the sweep and the withdrawals act
	// only on a mapping whose upstream the address record vouches for, so
	// nothing of the operator's is ever turned off.
	ServedTailnetPorts(ctx context.Context) (map[int]int, error)
}

// tailnetActivationEvidence is what activation records about the preview's
// tailnet address. Reachable is advisory: the node's certificate is minted
// on the first request, so the probe can fail once while the URL works a
// moment later.
type tailnetActivationEvidence struct {
	URL        string `json:"url"`
	Port       int    `json:"port"`
	Reachable  bool   `json:"reachable"`
	ProbeError string `json:"probeError,omitempty"`
}

// TailnetSweep is what SweepTailnet found and put right, for the caller's
// log: the served loopback ports in the preview range that the address
// table did not vouch for, now withdrawn, and the previews whose address
// was recorded as live although tailscale no longer served what the record
// says.
type TailnetSweep struct {
	Withdrawn   []int
	Unpublished []int64
}

const previewAddressColumns = `kind, port, upstream_port, url, published`

func scanPreviewAddress(row scanner) (*PreviewAddress, error) {
	var address PreviewAddress
	var published int
	if err := row.Scan(&address.Kind, &address.Port, &address.UpstreamPort, &address.URL, &published); err != nil {
		return nil, err
	}
	address.Published = published != 0
	return &address, nil
}

// PreviewAddressFor returns the environment's address, or nil when it has
// none: production environments and webhook previews with a domain pattern
// never have one.
func (s *OrchestrationStore) PreviewAddressFor(ctx context.Context, environmentID int64) (*PreviewAddress, error) {
	address, err := scanPreviewAddress(s.db.QueryRowContext(ctx,
		`SELECT `+previewAddressColumns+` FROM deploy_preview_addresses WHERE environment_id = ?`, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return address, err
}

// AllocatePreviewAddress gives a preview environment its tailnet port: the
// one it already has, else the lowest free port of the range. exclude is
// every port tailscale serves for anything at all — membership counts,
// whatever the value says about the mapping — so a new preview never claims
// one of those and then fails to publish. The choice and the insert share
// one transaction under the store's write lock; UNIQUE(kind, port) is the
// cross-process authority, and a conflict there simply picks again.
func (s *OrchestrationStore) AllocatePreviewAddress(ctx context.Context, environmentID int64, exclude map[int]int) (*PreviewAddress, error) {
	for attempt := 0; attempt < 8; attempt++ {
		address, err := s.allocatePreviewAddressOnce(ctx, environmentID, exclude)
		if err == nil || !isUniqueConstraint(err) {
			return address, err
		}
	}
	return nil, ErrPreviewAddressExhausted
}

func (s *OrchestrationStore) allocatePreviewAddressOnce(ctx context.Context, environmentID int64, exclude map[int]int) (*PreviewAddress, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	existing, err := scanPreviewAddress(tx.QueryRowContext(ctx,
		`SELECT `+previewAddressColumns+` FROM deploy_preview_addresses WHERE environment_id = ?`, environmentID))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var kind EnvironmentKind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ?`, environmentID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	// Only a preview may answer on the tailnet: production keeps its own
	// ingress, and a port here would publish it past that.
	if kind != EnvironmentPreview {
		return nil, ErrPreviewIsolation
	}
	rows, err := tx.QueryContext(ctx, `SELECT port FROM deploy_preview_addresses WHERE kind = ?`, previewAddressTailnet)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			rows.Close()
			return nil, err
		}
		used[port] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	port := 0
	for candidate := previewTailnetPortMin; candidate <= previewTailnetPortMax; candidate++ {
		if _, served := exclude[candidate]; !used[candidate] && !served {
			port = candidate
			break
		}
	}
	if port == 0 {
		return nil, ErrPreviewAddressExhausted
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_preview_addresses(environment_id, kind, port, upstream_port, url, published, updated_at)
		VALUES(?, ?, ?, 0, '', 0, ?)`, environmentID, previewAddressTailnet, port, s.now().UTC().Unix()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &PreviewAddress{Kind: previewAddressTailnet, Port: port}, nil
}

// MarkPreviewAddressPublished records what the tailnet mapping points at
// now: the URL the operator opens, the loopback port behind it, which the
// next publish hands back as previousUpstream, and whether it is live.
func (s *OrchestrationStore) MarkPreviewAddressPublished(ctx context.Context, environmentID int64, url string, upstreamPort int, published bool) error {
	if upstreamPort < 0 || upstreamPort > 65535 {
		return fmt.Errorf("%w: upstream port %d is out of range", ErrInvalidPlan, upstreamPort)
	}
	flag := 0
	if published {
		flag = 1
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_preview_addresses SET url = ?, upstream_port = ?, published = ?, updated_at = ?
		 WHERE environment_id = ?`, url, upstreamPort, flag, s.now().UTC().Unix(), environmentID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrPreviewAddressMissing
	}
	return nil
}

// DeletePreviewAddress frees the port. It is quiet about a row that is
// already gone, so a retried removal run passes through it again.
func (s *OrchestrationStore) DeletePreviewAddress(ctx context.Context, environmentID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_preview_addresses WHERE environment_id = ?`, environmentID)
	return err
}

// ListPreviewAddresses returns every address keyed by its environment, for
// the startup sweep that compares the table with tailscale's serve config.
func (s *OrchestrationStore) ListPreviewAddresses(ctx context.Context) (map[int64]PreviewAddress, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT environment_id, `+previewAddressColumns+` FROM deploy_preview_addresses ORDER BY port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	addresses := map[int64]PreviewAddress{}
	for rows.Next() {
		var environmentID int64
		var address PreviewAddress
		var published int
		if err := rows.Scan(&environmentID, &address.Kind, &address.Port, &address.UpstreamPort, &address.URL, &published); err != nil {
			return nil, err
		}
		address.Published = published != 0
		addresses[environmentID] = address
	}
	return addresses, rows.Err()
}

// publishPreviewAddress maps the preview's tailnet port to the candidate's
// loopback port and records the mapping. A non-nil result is the failure
// the activation step returns after it has stopped the candidate.
func (e *NormalizedStepExecutor) publishPreviewAddress(
	ctx context.Context,
	execution StepExecution,
	environmentID int64,
	address PreviewAddress,
	runtime ReleaseRuntime,
) (*tailnetActivationEvidence, *StepResult) {
	if e.tailnet == nil {
		return nil, &StepResult{State: StepUnavailable, ErrorCode: "tailnet_unavailable",
			ErrorMessage: "this preview answers on the tailnet, but Tailscale Serve is unavailable on this host"}
	}
	if runtime.Port == 0 {
		return nil, &StepResult{State: StepFailed, ErrorCode: "preview_port_missing",
			ErrorMessage: "the runtime plan has no internal port to publish"}
	}
	url, err := e.tailnet.PublishTailnet(ctx, address.Port, runtime.Port, address.UpstreamPort)
	if err != nil {
		failure := runtimeStepFailure(err, "tailnet_publish_failed",
			"the preview could not be published on the tailnet: "+err.Error(), nil)
		return nil, &failure
	}
	// The mapping is live from here on, so the record follows it even when
	// the run is being cancelled; otherwise the next publish could not tell
	// this dashboard's own mapping from a foreign one. For the same reason a
	// mapping whose record failed comes down before the failure is reported:
	// left standing, it would refuse every later publish of this preview as
	// somebody else's.
	if err := e.store.MarkPreviewAddressPublished(context.WithoutCancel(ctx), environmentID, url, runtime.Port, true); err != nil {
		message := "the preview was published on the tailnet, but its address could not be recorded: " + err.Error()
		if withdrawErr := e.tailnet.WithdrawTailnet(context.WithoutCancel(ctx), address.Port); withdrawErr != nil {
			message += "; the mapping could not be withdrawn either: " + withdrawErr.Error()
		}
		return nil, &StepResult{State: StepFailed, ErrorCode: "tailnet_publish_failed", ErrorMessage: message}
	}
	_ = stepLog(execution, "status", "Published on the tailnet at "+url)
	evidence := &tailnetActivationEvidence{URL: url, Port: address.Port}
	if err := e.probeTailnet(ctx, url); err != nil {
		evidence.ProbeError = err.Error()
		_ = stepLog(execution, "status", "The address did not answer yet; its certificate is issued on the first request: "+err.Error())
		return evidence, nil
	}
	evidence.Reachable = true
	_ = stepLog(execution, "status", "The address answered a request over the tailnet")
	return evidence, nil
}

func (e *NormalizedStepExecutor) probeTailnet(ctx context.Context, url string) error {
	if e.tailnetProbe != nil {
		return e.tailnetProbe(ctx, url)
	}
	return probeTailnetURL(ctx, url)
}

var tailnetProbeClient = &http.Client{
	Transport:     &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// probeTailnetURL asks the published address for any answer at all. TLS
// stays verified: the node's certificate comes from a public CA.
func probeTailnetURL(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := tailnetProbeClient.Do(request)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// restoreTailnet puts the preview's tailnet mapping back to what the live
// state deserves after a failed cutover: the predecessor's port while a
// predecessor still runs, nothing at all when the failed candidate would
// have been the first release or the predecessor could not be started
// again. It reports whether that state was reached. predecessorRunning is
// the recovery's own finding, since a mapping pointed at a stopped
// predecessor would be recorded as reachable while answering nothing.
func (e *NormalizedStepExecutor) restoreTailnet(ctx context.Context, execution StepExecution, release Release, predecessorRunning bool) bool {
	address, err := e.store.PreviewAddressFor(ctx, release.EnvironmentID)
	if err != nil {
		return false
	}
	if address == nil || address.Kind != previewAddressTailnet {
		return true
	}
	// Without a publisher nothing here could have changed the mapping, so
	// there is nothing to put back.
	if e.tailnet == nil {
		return true
	}
	if release.PredecessorReleaseID != 0 && predecessorRunning {
		predecessor, err := e.store.RuntimeForRelease(ctx, release.PredecessorReleaseID)
		if err == nil && predecessor.Port != 0 {
			url, err := e.tailnet.PublishTailnet(ctx, address.Port, predecessor.Port, address.UpstreamPort)
			if err != nil {
				_ = stepLog(execution, "stderr", "The tailnet address could not be pointed back at the previous release: "+err.Error())
				return false
			}
			if err := e.store.MarkPreviewAddressPublished(ctx, release.EnvironmentID, url, predecessor.Port, true); err != nil {
				return false
			}
			_ = stepLog(execution, "status", "Pointed the tailnet address back at the previous release")
			return true
		}
	}
	served, err := e.tailnet.ServedTailnetPorts(ctx)
	if err != nil {
		_ = stepLog(execution, "stderr", "The tailnet serve config could not be read: "+err.Error())
		return false
	}
	if ownsServedTailnetPort(*address, served) {
		if err := e.tailnet.WithdrawTailnet(ctx, address.Port); err != nil {
			_ = stepLog(execution, "stderr", "The tailnet address could not be withdrawn: "+err.Error())
			return false
		}
		_ = stepLog(execution, "status", "Withdrew the tailnet address")
	}
	if err := e.store.MarkPreviewAddressPublished(ctx, release.EnvironmentID, address.URL, 0, false); err != nil {
		return false
	}
	return true
}

// ownsServedTailnetPort says whether what tailscale serves on the address's
// port is the mapping the record made: a proxy to the very loopback port the
// row carries. An unpublished row vouches for nothing, and a mapping the
// operator made or re-pointed on the port is theirs to keep, so both read as
// not ours; a withdrawal then frees the row and leaves the mapping alone.
func ownsServedTailnetPort(address PreviewAddress, served map[int]int) bool {
	return address.UpstreamPort != 0 && served[address.Port] == address.UpstreamPort
}

// withdrawPreviewAddress takes the preview's port off the tailnet and frees
// it. It reports whether the environment had an address at all.
func (e *NormalizedStepExecutor) withdrawPreviewAddress(ctx context.Context, environmentID int64) (bool, error) {
	address, err := e.store.PreviewAddressFor(ctx, environmentID)
	if err != nil || address == nil {
		return false, err
	}
	if address.Kind == previewAddressTailnet && e.tailnet != nil {
		served, err := e.tailnet.ServedTailnetPorts(ctx)
		if err != nil {
			return true, err
		}
		if ownsServedTailnetPort(*address, served) {
			if err := e.tailnet.WithdrawTailnet(ctx, address.Port); err != nil {
				return true, err
			}
		}
	}
	return true, e.store.DeletePreviewAddress(ctx, environmentID)
}

func tailnetWithdrawFailure(err error) StepResult {
	return runtimeStepFailure(err, "tailnet_withdraw_failed",
		"the preview's tailnet address could not be withdrawn: "+err.Error(), nil)
}

// SweepTailnet reconciles tailscale's serve config with the address table
// after a restart. A crash between a publish and its record, or a removal
// run that never finished, leaves a served loopback port the table does not
// vouch for — no row owns it, or the row records another upstream or none —
// or a preview recorded as live that nothing serves. The first is withdrawn,
// since left standing it would refuse every later publish of that preview
// as somebody else's; the second is marked unpublished so the UI asks for a
// redeploy instead of offering a dead link.
func (e *NormalizedStepExecutor) SweepTailnet(ctx context.Context) (TailnetSweep, error) {
	sweep := TailnetSweep{}
	if e == nil || e.store == nil || e.tailnet == nil {
		return sweep, nil
	}
	// The table first, the serve config second, so no mapping is judged
	// against a record older than itself; the caller sweeps before the
	// engine starts, so nothing publishes while the two are compared.
	addresses, err := e.store.ListPreviewAddresses(ctx)
	if err != nil {
		return sweep, err
	}
	served, err := e.tailnet.ServedTailnetPorts(ctx)
	if err != nil {
		// A serve config that cannot be read is only worth a warning when a
		// preview is recorded as live on it; a host without Tailscale has
		// no such preview and must not be warned about at every boot.
		for _, address := range addresses {
			if address.Kind == previewAddressTailnet && address.Published {
				return sweep, err
			}
		}
		return sweep, nil
	}
	owned := make(map[int]bool, len(addresses))
	environments := make([]int64, 0, len(addresses))
	for environmentID, address := range addresses {
		environments = append(environments, environmentID)
		if address.Kind == previewAddressTailnet {
			owned[address.Port] = true
		}
	}
	ports := make([]int, 0, len(served))
	for port, upstream := range served {
		if upstream != 0 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	sort.Slice(environments, func(i, j int) bool { return environments[i] < environments[j] })
	var errs []error
	for _, port := range ports {
		if port < previewTailnetPortMin || port > previewTailnetPortMax || owned[port] {
			continue
		}
		if err := e.tailnet.WithdrawTailnet(ctx, port); err != nil {
			errs = append(errs, fmt.Errorf("tailnet port %d: %w", port, err))
			continue
		}
		sweep.Withdrawn = append(sweep.Withdrawn, port)
	}
	for _, environmentID := range environments {
		address := addresses[environmentID]
		if address.Kind != previewAddressTailnet {
			continue
		}
		upstream := served[address.Port]
		if address.Published && upstream == address.UpstreamPort {
			continue
		}
		// A loopback mapping the row does not vouch for is a publish whose
		// record never landed; only the dashboard maps its own range to
		// loopback, so it comes down before it wedges the preview.
		if upstream != 0 {
			if err := e.tailnet.WithdrawTailnet(ctx, address.Port); err != nil {
				errs = append(errs, fmt.Errorf("tailnet port %d: %w", address.Port, err))
				continue
			}
			sweep.Withdrawn = append(sweep.Withdrawn, address.Port)
		}
		if !address.Published {
			continue
		}
		if err := e.store.MarkPreviewAddressPublished(ctx, environmentID, address.URL, 0, false); err != nil {
			errs = append(errs, fmt.Errorf("preview environment %d: %w", environmentID, err))
			continue
		}
		sweep.Unpublished = append(sweep.Unpublished, environmentID)
	}
	return sweep, errors.Join(errs...)
}

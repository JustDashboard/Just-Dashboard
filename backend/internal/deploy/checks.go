package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
)

type HealthOutcome string

const (
	HealthDisabled    HealthOutcome = "disabled"
	HealthUnavailable HealthOutcome = "unavailable"
	HealthWarning     HealthOutcome = "warning"
	HealthPassed      HealthOutcome = "passed"
	HealthFailed      HealthOutcome = "failed"
)

type CheckConfiguration struct {
	Disabled        bool     `json:"disabled,omitempty"`
	URL             string   `json:"url,omitempty"`
	Path            string   `json:"path,omitempty"`
	Host            string   `json:"host,omitempty"`
	Port            int      `json:"port,omitempty"`
	Method          string   `json:"method,omitempty"`
	ExpectedStatus  []int    `json:"expectedStatus,omitempty"`
	Command         []string `json:"command,omitempty"`
	Attempts        int      `json:"attempts,omitempty"`
	TimeoutSeconds  int      `json:"timeoutSeconds,omitempty"`
	IntervalSeconds int      `json:"intervalSeconds,omitempty"`

	// AcceptAnyAnswer counts any answer a serving application gives — a 404
	// from an API with no page at the path, a 401 from a login wall, a
	// redirect to another site — as ready; only a 5xx, a 400 or 421 (what host
	// allowlists answer) or no answer at all is not. Detection sets it for
	// services whose root is not a page, where 2xx-only failed healthy
	// releases (checks_http.go).
	AcceptAnyAnswer bool `json:"acceptAnyAnswer,omitempty"`

	// Payload is the datagram a udp_probe sends and Expect the prefix its
	// answer must begin with, both lowercase hexadecimal. An empty Expect
	// accepts any answer: for a UDP server, answering is the whole signal.
	Payload string `json:"payload,omitempty"`
	Expect  string `json:"expect,omitempty"`
}

// Probe bounds, as the catalogue holds a definition to them.
const (
	maxUDPProbePayload = 512
	maxUDPProbeExpect  = 64
	maxUDPAnswerBytes  = 2048
)

var probeHexRE = regexp.MustCompile(`^(?:[0-9a-f]{2})*$`)

func decodeCheckConfiguration(raw json.RawMessage) (CheckConfiguration, error) {
	config := CheckConfiguration{}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("check configuration has unsupported or malformed fields")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return config, errors.New("check configuration has trailing data")
	}
	return config, nil
}

func validateCheckConfiguration(kind string, raw json.RawMessage) error {
	config, err := decodeCheckConfiguration(raw)
	if err != nil {
		return err
	}
	if config.Attempts < 0 || config.Attempts > 60 || config.TimeoutSeconds < 0 || config.TimeoutSeconds > 60 ||
		config.IntervalSeconds < 0 || config.IntervalSeconds > 60 {
		return errors.New("attempts and timing are outside the supported bounds")
	}
	if config.Port < 0 || config.Port > 65535 || strings.ContainsAny(config.Host, "\x00\r\n") ||
		strings.ContainsAny(config.Path, "\x00\r\n") {
		return errors.New("host, port, or path is invalid")
	}
	if config.Path != "" && !strings.HasPrefix(config.Path, "/") {
		return errors.New("HTTP path must begin with /")
	}
	if config.Method != "" && config.Method != http.MethodGet && config.Method != http.MethodHead {
		return errors.New("HTTP method must be GET or HEAD")
	}
	for _, status := range config.ExpectedStatus {
		if status < 100 || status > 599 {
			return errors.New("expected HTTP status is invalid")
		}
	}
	if config.AcceptAnyAnswer && len(config.ExpectedStatus) != 0 {
		return errors.New("an HTTP check expects either listed statuses or any answer, not both")
	}
	if !probeHexRE.MatchString(config.Payload) || len(config.Payload)/2 > maxUDPProbePayload ||
		!probeHexRE.MatchString(config.Expect) || len(config.Expect)/2 > maxUDPProbeExpect {
		return errors.New("a UDP probe's payload and expected answer are bounded lowercase hexadecimal")
	}
	if (config.Payload != "" || config.Expect != "") && CheckKind(kind) != CheckUDPProbe {
		return errors.New("only a UDP probe sends a datagram")
	}
	if config.URL != "" {
		parsed, err := url.Parse(config.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
			return errors.New("check URL must be an HTTP(S) URL without credentials, query, or fragment")
		}
	}
	for index, argument := range config.Command {
		if argument == "" || len(argument) > 4096 || strings.ContainsRune(argument, '\x00') ||
			commandArgumentContainsSecret(config.Command, index) {
			return errors.New("command check must use secret-free argv")
		}
	}
	if config.Disabled {
		return nil
	}
	switch CheckKind(kind) {
	case CheckHTTP, CheckPublicRoute:
		if len(config.Command) != 0 {
			return errors.New("HTTP checks cannot include a command")
		}
	case CheckTCP, CheckDockerHealth:
		if config.URL != "" || len(config.Command) != 0 || config.AcceptAnyAnswer {
			return errors.New("TCP/Docker-health checks contain unrelated fields")
		}
	case CheckCommand:
		if len(config.Command) == 0 || config.URL != "" || config.AcceptAnyAnswer {
			return errors.New("command checks require a non-empty argv")
		}
	case CheckGameHandshake, CheckUDPProbe:
		if config.URL != "" || len(config.Command) != 0 || config.AcceptAnyAnswer || config.Path != "" ||
			len(config.ExpectedStatus) != 0 || config.Method != "" {
			return errors.New("a game handshake or UDP probe names a host and port, and nothing an HTTP or command check takes")
		}
	default:
		// DNS/TLS/backup checks have no runner (see unrunnableCheck). A
		// plan saved with one still validates so it keeps deploying, but
		// accepting C5 fields here would make it look executable.
		if config.URL != "" || config.Host != "" || config.Port != 0 || len(config.Command) != 0 || config.AcceptAnyAnswer {
			return errors.New("this check kind has no C5 execution configuration")
		}
	}
	return nil
}

// unrunnableCheck says why the runner would never execute a check of this
// kind, or "" for a kind it runs. Such a check only ever reports
// check_owner_unavailable, which blocks the phase when the check is required
// and is a warning when it is not.
func unrunnableCheck(kind string) string {
	switch CheckKind(kind) {
	case CheckDNS, CheckTLS:
		return "the deployment runner has no " + kind + " check; a public_route check on the https address resolves the name and verifies the certificate"
	case CheckBackupFreshness:
		return "backup freshness is set on a storage dependency (requiredBeforeDeploy, maxAgeSeconds, requireRestoreTest), not as a check"
	}
	return ""
}

// refuseUnrunnableChecks keeps a configuration being written from asking for
// a check nothing runs. It applies where a configuration is saved, not in
// Validate: a stored plan that already holds one is re-validated before it
// runs, and refusing it there would stop a deployment that runs today.
func refuseUnrunnableChecks(checks []PlannedCheck) error {
	for index, check := range checks {
		if reason := unrunnableCheck(check.Kind); reason != "" {
			return invalidField(fmt.Sprintf("checks[%d]", index), "check %q of kind %s cannot run: %s", check.Name, check.Kind, reason)
		}
	}
	return nil
}

type CheckTarget struct {
	OriginalPorts []int
	ContainerID   string
	Host          string
	Port          int
	PublicURLs    []string
	// ServiceRoute is a public-route check asked of a service domain
	// (service_domains.go): the application behind it answers with its own
	// sign-in as often as not, so any answer below 500 is reachable.
	ServiceRoute bool
}

type CheckAttemptEvidence struct {
	Number         int           `json:"number"`
	Outcome        HealthOutcome `json:"outcome"`
	DurationMillis int64         `json:"durationMillis"`
	Address        string        `json:"address,omitempty"`
	StatusCode     int           `json:"statusCode,omitempty"`
	ContainerState string        `json:"containerState,omitempty"`
	ExitCode       int           `json:"exitCode,omitempty"`
	// Restarts is how often the candidate's container had died and been
	// started again when the gate stopped waiting for it.
	Restarts     int    `json:"restarts,omitempty"`
	OutputDigest string `json:"outputDigest,omitempty"`
	Code         string `json:"code,omitempty"`

	// RedirectOrigin is where an unfollowed redirect pointed: its scheme and
	// host only, since a login redirect's query carries state tokens.
	RedirectOrigin string `json:"redirectOrigin,omitempty"`

	// Repeats is how many further attempts gave this one's answer straight
	// after it and are folded into it, LastNumber the last of them and
	// SpanMillis the time from this attempt's start to the end of that one:
	// early polling asks every quarter second, and forty identical refusals
	// say no more than "connection refused × 40 over 9.8 s".
	Repeats    int   `json:"repeats,omitempty"`
	LastNumber int   `json:"lastNumber,omitempty"`
	SpanMillis int64 `json:"spanMillis,omitempty"`
}

type CheckEvidence struct {
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"`
	Phase       string                 `json:"phase"`
	Required    bool                   `json:"required"`
	Outcome     HealthOutcome          `json:"outcome"`
	Attempts    []CheckAttemptEvidence `json:"attempts"`
	StartedAt   time.Time              `json:"startedAt"`
	CompletedAt time.Time              `json:"completedAt"`
	// Replica is which of a replicated release's containers the check
	// asked, from 1; zero for a release of one container and for a check of
	// the public route, which asks whichever replica the proxy picks.
	Replica int `json:"replica,omitempty"`
}

// AttemptCount is how many times the check asked, counting the attempts
// folded into a kept one.
func (e CheckEvidence) AttemptCount() int {
	count := 0
	for _, attempt := range e.Attempts {
		count += 1 + attempt.Repeats
	}
	return count
}

type ContainerCheckBackend interface {
	ContainerHealth(context.Context, string) (string, error)
	ExecCheck(context.Context, string, []string, time.Duration) (int, []byte, error)
}

type CheckRunner struct {
	containers ContainerCheckBackend
	http       *http.Client
	dial       func(context.Context, string, string) (net.Conn, error)
	now        func() time.Time
	// sleep waits between attempts, returning early when ctx ends; tests
	// replace it to move a fake clock instead.
	sleep func(context.Context, time.Duration)
}

// A check with an interval asks quickly at first: an application that comes
// up 3.2 s after it starts used to be found at 6 s, on the next 3 s tick.
// For the first earlyPollingWindow it asks every earlyPollingInterval, or as
// long as its last attempt took when that is longer, so an application that
// answers slowly is never asked again before it has answered; after the
// window it asks at the configured interval. The waits add up to the same
// budget as before — (attempts − 1) × interval — so a check that never
// passes gives up when it did.
const (
	earlyPollingWindow   = 10 * time.Second
	earlyPollingInterval = 250 * time.Millisecond
)

func sleepContext(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// checkPollWait is how long to wait before the next attempt: quick during
// the early window, never shorter than the attempt that just ran, never
// longer than the interval, and never past what is left of the budget.
func checkPollWait(sinceStart, lastAttempt, interval, remaining time.Duration) time.Duration {
	wait := interval
	if sinceStart < earlyPollingWindow && interval > earlyPollingInterval {
		wait = max(earlyPollingInterval, lastAttempt)
		wait = min(wait, interval)
	}
	return min(wait, remaining)
}

// containerLivenessReader is the part of a check backend that reads a
// candidate's state, which the Docker client has.
type containerLivenessReader interface {
	ContainerLiveness(context.Context, string) (dockerx.ContainerLiveness, error)
}

// A candidate that dies and is started again over and over is given up on
// before the attempts run out, but not early: an application that exits while
// the database it waits for is still starting gets its first thirty seconds
// and five restarts before the gate reads that as a loop.
const (
	crashLoopRestarts = 5
	crashLoopGrace    = 30 * time.Second
)

// candidateGone says the target's container is down for good, or has been
// dying and restarting long enough to read as a loop, so asking it again only
// waits out the attempts: a start command that fails before the server
// listens would otherwise read as "could not connect" two minutes later.
func (r *CheckRunner) candidateGone(ctx context.Context, target CheckTarget, started time.Time) (dockerx.ContainerLiveness, bool) {
	reader, ok := r.containers.(containerLivenessReader)
	if !ok || target.ContainerID == "" {
		return dockerx.ContainerLiveness{}, false
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, err := reader.ContainerLiveness(readCtx, target.ContainerID)
	if err != nil {
		return state, false
	}
	switch {
	// Restarting comes first: while its restart policy backs off, Docker
	// reports a dying container as running and restarting at once.
	case state.Restarting:
		return state, state.RestartCount >= crashLoopRestarts && r.now().Sub(started) >= crashLoopGrace
	case state.Running:
		return state, false
	case state.Status == "exited" || state.Status == "dead":
		// Docker marks a container restarting the moment it exits when its
		// policy starts it again, so exited means nothing will.
		return state, true
	}
	return state, false
}

func NewCheckRunner(containers ContainerCheckBackend) *CheckRunner {
	dialer := &net.Dialer{}
	return &CheckRunner{
		containers: containers,
		http: &http.Client{
			Transport:     &http.Transport{Proxy: nil, DialContext: dialer.DialContext},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		dial:  dialer.DialContext,
		now:   time.Now,
		sleep: sleepContext,
	}
}

func (r *CheckRunner) Run(ctx context.Context, check PlannedCheck, target CheckTarget) CheckEvidence {
	started := r.now().UTC()
	evidence := CheckEvidence{
		Name: check.Name, Kind: check.Kind, Phase: check.Phase, Required: check.Required,
		Attempts: []CheckAttemptEvidence{}, StartedAt: started,
	}
	config, err := decodeCheckConfiguration(check.Config)
	if err != nil {
		evidence.Outcome = HealthFailed
		evidence.Attempts = append(evidence.Attempts, CheckAttemptEvidence{Number: 1, Outcome: HealthFailed, Code: "invalid_configuration"})
		evidence.CompletedAt = r.now().UTC()
		return evidence
	}
	if config.Disabled {
		evidence.Outcome = HealthDisabled
		evidence.CompletedAt = r.now().UTC()
		return evidence
	}
	attempts := config.Attempts
	if attempts == 0 {
		attempts = 10
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	interval := time.Duration(config.IntervalSeconds) * time.Second
	// Without an interval the attempts run back to back and their number is
	// the bound; with one, the waits between them are.
	// The extra attempts early polling makes add their own time, so the
	// check also stops where the configured attempts would have at the
	// latest, every one of them timing out (what Settings shows as the time
	// a check gives up after).
	budget, waited := time.Duration(attempts-1)*interval, time.Duration(0)
	latest := budget + time.Duration(attempts)*timeout
	timed := []timedAttempt{}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			break
		}
		attemptStarted := r.now()
		result := r.runAttempt(ctx, CheckKind(check.Kind), config, target, timeout)
		result.Number = attempt
		attemptEnded := r.now()
		done := result.Outcome == HealthPassed || result.Outcome == HealthDisabled || result.Outcome == HealthUnavailable
		if result.Outcome == HealthFailed {
			if state, gone := r.candidateGone(ctx, target, started); gone {
				result.Code, result.ContainerState = "application_exited", state.Status
				result.ExitCode, result.Restarts = state.ExitCode, state.RestartCount
				done = true
			}
		}
		timed = append(timed, timedAttempt{result, attemptStarted, attemptEnded})
		if done || interval == 0 && attempt >= attempts || interval > 0 && (waited >= budget || attemptEnded.Sub(started) >= latest) {
			break
		}
		if interval > 0 {
			wait := checkPollWait(attemptEnded.Sub(started), attemptEnded.Sub(attemptStarted), interval, budget-waited)
			r.sleep(ctx, wait)
			waited += wait
		}
	}
	evidence.Attempts = append(evidence.Attempts, collapseAttempts(timed)...)
	evidence.Outcome = HealthFailed
	if len(evidence.Attempts) == 0 {
		evidence.Attempts = append(evidence.Attempts, CheckAttemptEvidence{Number: 1, Outcome: HealthFailed, Code: "cancelled"})
	} else {
		evidence.Outcome = evidence.Attempts[len(evidence.Attempts)-1].Outcome
	}
	if !check.Required && (evidence.Outcome == HealthFailed || evidence.Outcome == HealthUnavailable) {
		evidence.Outcome = HealthWarning
	}
	evidence.CompletedAt = r.now().UTC()
	return evidence
}

// timedAttempt is an attempt with when it ran, which folding its repeats
// into the one before needs and the evidence keeps only as a span.
type timedAttempt struct {
	attempt        CheckAttemptEvidence
	started, ended time.Time
}

// sameAnswer says two attempts got the same answer: everything the evidence
// says about it but how long it took.
func sameAnswer(a, b CheckAttemptEvidence) bool {
	return a.Outcome == b.Outcome && a.Code == b.Code && a.StatusCode == b.StatusCode && a.Address == b.Address &&
		a.ContainerState == b.ContainerState && a.ExitCode == b.ExitCode && a.Restarts == b.Restarts &&
		a.OutputDigest == b.OutputDigest && a.RedirectOrigin == b.RedirectOrigin
}

// collapseAttempts keeps the first attempt, the last, and the first of every
// run of a different answer, folding each run's repeats into its first
// (Repeats, LastNumber, SpanMillis). The last attempt is always its own
// entry, since the gate's outcome and message are read from it.
func collapseAttempts(timed []timedAttempt) []CheckAttemptEvidence {
	kept := make([]CheckAttemptEvidence, 0, min(len(timed), 4))
	var runStarted time.Time
	for index, current := range timed {
		last := len(kept) - 1
		if index > 0 && index < len(timed)-1 && sameAnswer(kept[last], current.attempt) {
			kept[last].Repeats++
			kept[last].LastNumber = current.attempt.Number
			kept[last].SpanMillis = current.ended.Sub(runStarted).Milliseconds()
			continue
		}
		kept = append(kept, current.attempt)
		runStarted = current.started
	}
	return kept
}

func (r *CheckRunner) runAttempt(
	ctx context.Context,
	kind CheckKind,
	config CheckConfiguration,
	target CheckTarget,
	timeout time.Duration,
) CheckAttemptEvidence {
	started := r.now()
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := CheckAttemptEvidence{Outcome: HealthFailed}
	switch kind {
	case CheckHTTP, CheckPublicRoute:
		r.httpAttempt(attemptCtx, kind, config, target, &result)
	case CheckTCP:
		host, port := targetAddress(config, target)
		if host == "" || port == 0 {
			result.Outcome, result.Code = HealthUnavailable, "target_unavailable"
			break
		}
		result.Address = net.JoinHostPort(host, strconv.Itoa(port))
		connection, err := r.dial(attemptCtx, "tcp", result.Address)
		if err != nil {
			result.Code = checkNetworkError(attemptCtx, err)
			break
		}
		_ = connection.Close()
		result.Outcome = HealthPassed
	case CheckDockerHealth:
		if target.ContainerID == "" || r.containers == nil {
			result.Outcome, result.Code = HealthUnavailable, "container_unavailable"
			break
		}
		status, err := r.containers.ContainerHealth(attemptCtx, target.ContainerID)
		result.ContainerState = status
		if err != nil || status == "" || status == "none" {
			result.Outcome, result.Code = HealthUnavailable, "health_unavailable"
		} else if status == "healthy" {
			result.Outcome = HealthPassed
		} else {
			result.Code = "container_" + status
		}
	case CheckCommand:
		if target.ContainerID == "" || r.containers == nil {
			result.Outcome, result.Code = HealthUnavailable, "container_unavailable"
			break
		}
		exitCode, output, err := r.containers.ExecCheck(attemptCtx, target.ContainerID, config.Command, timeout)
		result.ExitCode = exitCode
		if len(output) != 0 {
			hash := sha256.Sum256(output)
			result.OutputDigest = "sha256:" + hex.EncodeToString(hash[:])
		}
		if err == nil && exitCode == 0 {
			result.Outcome = HealthPassed
		} else if errors.Is(err, context.DeadlineExceeded) || attemptCtx.Err() != nil {
			result.Code = "timeout"
		} else {
			result.Code = "exit_nonzero"
		}
	case CheckGameHandshake:
		r.handshakeAttempt(attemptCtx, config, target, &result)
	case CheckUDPProbe:
		r.udpAttempt(attemptCtx, config, target, &result)
	default:
		result.Outcome, result.Code = HealthUnavailable, "check_owner_unavailable"
	}
	result.DurationMillis = time.Since(started).Milliseconds()
	return result
}

// handshakeAttempt asks a Minecraft Java server for its status the way the
// client's server list does. A listening socket is not the answer: the
// server binds its port before the world is ready, and answers the status
// only once it can take a player.
func (r *CheckRunner) handshakeAttempt(ctx context.Context, config CheckConfiguration, target CheckTarget, result *CheckAttemptEvidence) {
	host, port := targetAddress(config, target)
	if host == "" || port == 0 {
		result.Outcome, result.Code = HealthUnavailable, "target_unavailable"
		return
	}
	result.Address = net.JoinHostPort(host, strconv.Itoa(port))
	connection, err := r.dial(ctx, "tcp", result.Address)
	if err != nil {
		result.Code = checkNetworkError(ctx, err)
		return
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := gameserver.JavaStatusHandshake(connection, host, port); err != nil {
		result.Code = "handshake_failed"
		if ctx.Err() != nil {
			result.Code = "timeout"
		}
		return
	}
	result.Outcome = HealthPassed
}

// udpAttempt sends the probe's datagram and waits for one back. UDP has no
// connection to refuse, so silence until the deadline is the only failure a
// server that is not up yet gives — unless the kernel answers for it, which a
// connected socket reads as a refusal.
func (r *CheckRunner) udpAttempt(ctx context.Context, config CheckConfiguration, target CheckTarget, result *CheckAttemptEvidence) {
	host, port := targetAddress(config, target)
	if host == "" || port == 0 {
		result.Outcome, result.Code = HealthUnavailable, "target_unavailable"
		return
	}
	payload, _ := hex.DecodeString(config.Payload)
	expect, _ := hex.DecodeString(config.Expect)
	result.Address = net.JoinHostPort(host, strconv.Itoa(port))
	connection, err := r.dial(ctx, "udp", result.Address)
	if err != nil {
		result.Code = checkNetworkError(ctx, err)
		return
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write(payload); err != nil {
		result.Code = checkNetworkError(ctx, err)
		return
	}
	answer := make([]byte, maxUDPAnswerBytes)
	read, err := connection.Read(answer)
	if err != nil {
		result.Code = checkNetworkError(ctx, err)
		if result.Code == "timeout" {
			result.Code = "no_answer"
		}
		return
	}
	if !bytes.HasPrefix(answer[:read], expect) {
		result.Code = "unexpected_answer"
		return
	}
	result.Outcome = HealthPassed
}

func targetAddress(config CheckConfiguration, target CheckTarget) (string, int) {
	host, port := strings.TrimSpace(config.Host), config.Port
	if host == "" {
		host = target.Host
	}
	if port == 0 {
		port = target.Port
	} else if config.Host == "" || config.Host == target.Host {
		// Saved checks may name the original container/publication port. The
		// runtime's concrete allocation is authoritative for that same service.
		for _, original := range target.OriginalPorts {
			if original > 0 && port == original && target.Port > 0 {
				port = target.Port
				break
			}
		}
	}
	return host, port
}

func expectedHTTPStatus(status int, expected []int) bool {
	if len(expected) == 0 {
		return status >= 200 && status < 300
	}
	for _, candidate := range expected {
		if status == candidate {
			return true
		}
	}
	return false
}

func checkNetworkError(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "connection_failed"
}

func summarizeChecks(checks []CheckEvidence) HealthOutcome {
	if len(checks) == 0 {
		return HealthDisabled
	}
	outcome := HealthPassed
	for _, check := range checks {
		switch check.Outcome {
		case HealthFailed:
			return HealthFailed
		case HealthUnavailable:
			if outcome != HealthFailed {
				outcome = HealthUnavailable
			}
		case HealthWarning:
			if outcome == HealthPassed || outcome == HealthDisabled {
				outcome = HealthWarning
			}
		case HealthDisabled:
			if outcome == HealthPassed {
				outcome = HealthDisabled
			}
		}
	}
	return outcome
}

func checkFailureMessage(phase string, outcome HealthOutcome, checks ...CheckEvidence) string {
	message := fmt.Sprintf("%s checks %s", phase, outcome)
	for _, check := range checks {
		if !check.Required || check.Outcome == HealthPassed || len(check.Attempts) == 0 {
			continue
		}
		last := check.Attempts[len(check.Attempts)-1]
		reason := "the check did not pass"
		switch last.Code {
		case "connection_failed":
			reason = "could not connect to the application"
		case "timeout":
			reason = "the application did not respond before the timeout"
		case "unexpected_status":
			reason = fmt.Sprintf("the application returned HTTP %d", last.StatusCode)
		case "redirect_off_origin":
			reason = fmt.Sprintf("the application redirected (HTTP %d) to %s, another site readiness does not follow",
				last.StatusCode, last.RedirectOrigin)
		case "redirect_loop":
			reason = fmt.Sprintf("the application kept redirecting (HTTP %d to %s)", last.StatusCode, last.RedirectOrigin)
		case "target_unavailable":
			reason = "the application's runtime address is unavailable"
		case "exit_nonzero":
			reason = fmt.Sprintf("the check command exited with code %d", last.ExitCode)
		case "application_exited":
			if last.Restarts > 0 {
				reason = fmt.Sprintf("the application kept exiting (code %d, restarted %d time(s)) instead of answering", last.ExitCode, last.Restarts)
			} else {
				reason = fmt.Sprintf("the application exited with code %d and nothing starts it again, so nothing answered", last.ExitCode)
			}
		case "handshake_failed":
			reason = "the game server accepted the connection but did not answer a status handshake"
		case "no_answer":
			reason = "the game server did not answer the probe datagram"
		case "unexpected_answer":
			reason = "the server answered the probe datagram with something other than the expected reply"
		}
		address := last.Address
		if parsed, err := url.Parse(address); err == nil && parsed.Host != "" {
			address = parsed.Host
		} else if _, _, err := net.SplitHostPort(address); err != nil {
			address = ""
		}
		if address != "" {
			reason += " at " + address
		}
		return fmt.Sprintf("%s: %s — %s after %d attempt(s)", message, check.Name, reason, check.AttemptCount())
	}
	return message
}

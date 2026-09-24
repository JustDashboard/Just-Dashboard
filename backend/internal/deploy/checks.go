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
	"strconv"
	"strings"
	"time"
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
}

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
	default:
		// DNS/TLS/game/backup checks are owned by later feature joins. Their
		// closed kind is already valid, but accepting arbitrary C5 fields here
		// would make an unavailable check look executable.
		if config.URL != "" || config.Host != "" || config.Port != 0 || len(config.Command) != 0 || config.AcceptAnyAnswer {
			return errors.New("this check kind has no C5 execution configuration")
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
}

type CheckAttemptEvidence struct {
	Number         int           `json:"number"`
	Outcome        HealthOutcome `json:"outcome"`
	DurationMillis int64         `json:"durationMillis"`
	Address        string        `json:"address,omitempty"`
	StatusCode     int           `json:"statusCode,omitempty"`
	ContainerState string        `json:"containerState,omitempty"`
	ExitCode       int           `json:"exitCode,omitempty"`
	OutputDigest   string        `json:"outputDigest,omitempty"`
	Code           string        `json:"code,omitempty"`

	// RedirectOrigin is where an unfollowed redirect pointed: its scheme and
	// host only, since a login redirect's query carries state tokens.
	RedirectOrigin string `json:"redirectOrigin,omitempty"`
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
}

func NewCheckRunner(containers ContainerCheckBackend) *CheckRunner {
	dialer := &net.Dialer{}
	return &CheckRunner{
		containers: containers,
		http: &http.Client{
			Transport:     &http.Transport{Proxy: nil, DialContext: dialer.DialContext},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		dial: dialer.DialContext,
		now:  time.Now,
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
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			break
		}
		result := r.runAttempt(ctx, CheckKind(check.Kind), config, target, timeout)
		result.Number = attempt
		evidence.Attempts = append(evidence.Attempts, result)
		if result.Outcome == HealthPassed || result.Outcome == HealthDisabled || result.Outcome == HealthUnavailable {
			break
		}
		if attempt < attempts && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
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
	default:
		result.Outcome, result.Code = HealthUnavailable, "check_owner_unavailable"
	}
	result.DurationMillis = time.Since(started).Milliseconds()
	return result
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
		return fmt.Sprintf("%s: %s — %s after %d attempt(s)", message, check.Name, reason, len(check.Attempts))
	}
	return message
}

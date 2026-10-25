package deploy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Notification events. A channel with an empty list receives every event; the
// historical "run.finished" selects every terminal outcome.
const (
	NotificationEventStarted   = "run.started"
	NotificationEventSucceeded = "run.succeeded"
	NotificationEventFailed    = "run.failed"
	NotificationEventCancelled = "run.cancelled"
	NotificationEventFinished  = "run.finished"
	NotificationEventTest      = "test"
	// The traffic events are not selected on a channel: an alert rule names
	// the channels it reaches, and these are what it sends them.
	NotificationEventTrafficFiring    = "traffic.firing"
	NotificationEventTrafficRecovered = "traffic.recovered"
	// The proxy events, likewise, are sent to the channels a proxy alert
	// rule names rather than selected on a channel.
	NotificationEventProxyFiring    = "proxy.alert.firing"
	NotificationEventProxyRecovered = "proxy.alert.recovered"
	// And the database events, to the channels a database alert rule names.
	NotificationEventDatabaseFiring    = "database.alert.firing"
	NotificationEventDatabaseRecovered = "database.alert.recovered"
	// And the host events, to the channels a host alert rule names.
	NotificationEventHostFiring    = "host.alert.firing"
	NotificationEventHostRecovered = "host.alert.recovered"
)

// NotificationEvents is the closed vocabulary a channel may select from.
var NotificationEvents = []string{
	NotificationEventStarted, NotificationEventSucceeded, NotificationEventFailed, NotificationEventCancelled,
}

// Notification channel kinds. The webhook kind is the signed generic contract
// the product always had; the rest deliver a rendered message to a provider.
const (
	NotificationKindWebhook    = "webhook"
	NotificationKindDiscord    = "discord"
	NotificationKindSlack      = "slack"
	NotificationKindTelegram   = "telegram"
	NotificationKindEmail      = "email"
	NotificationKindNtfy       = "ntfy"
	NotificationKindGotify     = "gotify"
	NotificationKindPushover   = "pushover"
	NotificationKindMattermost = "mattermost"
	NotificationKindTeams      = "teams"
)

var NotificationKinds = []string{
	NotificationKindWebhook, NotificationKindDiscord, NotificationKindSlack, NotificationKindTelegram, NotificationKindEmail,
	NotificationKindNtfy, NotificationKindGotify, NotificationKindPushover, NotificationKindMattermost, NotificationKindTeams,
}

// NotificationConfig is a channel's per-kind configuration. It is sealed at
// rest as one JSON document because most of it is a credential: a Discord or
// Slack webhook URL authorizes posting, a bot token authorizes a bot, an SMTP
// password authorizes mail. Lists and API responses never include it.
type NotificationConfig struct {
	WebhookURL   string   `json:"webhookUrl,omitempty"`
	BotToken     string   `json:"botToken,omitempty"`
	ChatID       string   `json:"chatId,omitempty"`
	SMTPHost     string   `json:"smtpHost,omitempty"`
	SMTPPort     int      `json:"smtpPort,omitempty"`
	SMTPUsername string   `json:"smtpUsername,omitempty"`
	SMTPPassword string   `json:"smtpPassword,omitempty"`
	SMTPSecurity string   `json:"smtpSecurity,omitempty"`
	From         string   `json:"from,omitempty"`
	To           []string `json:"to,omitempty"`
	// ServerURL is an ntfy or Gotify server; Topic the ntfy topic, which on a
	// public server is the only thing standing between a stranger and the
	// messages. Token is an ntfy access token, a Gotify application token or a
	// Pushover application token, and UserKey the Pushover user or group key.
	ServerURL string `json:"serverUrl,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Token     string `json:"token,omitempty"`
	UserKey   string `json:"userKey,omitempty"`
}

// SMTP transport security modes.
const (
	SMTPSecurityStartTLS = "starttls"
	SMTPSecurityTLS      = "tls"
	SMTPSecurityNone     = "none"
)

var (
	telegramTokenRE           = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)
	telegramChatRE            = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
	smtpHostnameRE            = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*$`)
	maxNotificationRecipients = 20
	// The shapes each provider documents: ntfy's topic names and its tk_
	// access tokens, Gotify's application tokens, Pushover's 30-character
	// keys, and a Mattermost incoming webhook's /hooks/<id> path, which may sit
	// under the subpath a server is published at.
	ntfyTopicRE      = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)
	ntfyTokenRE      = regexp.MustCompile(`^tk_[A-Za-z0-9]{29}$`)
	gotifyTokenRE    = regexp.MustCompile(`^[A-Za-z0-9._-]{10,64}$`)
	pushoverKeyRE    = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	mattermostHookRE = regexp.MustCompile(`(^|/)hooks/[A-Za-z0-9]{20,64}$`)
)

// DefaultNtfyServer is where an ntfy channel posts when no server is named.
const DefaultNtfyServer = "https://ntfy.sh"

// ErrInvalidNotification reports a channel definition that cannot deliver.
var ErrInvalidNotification = fmt.Errorf("invalid notification channel")

func invalidNotification(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNotification, fmt.Sprintf(format, args...))
}

func validNotificationKind(kind string) bool {
	for _, known := range NotificationKinds {
		if kind == known {
			return true
		}
	}
	return false
}

// normalizeNotificationEvents rejects unknown selections; an empty list is the
// explicit "everything" and is kept as such.
func normalizeNotificationEvents(events []string) ([]string, error) {
	out := make([]string, 0, len(events))
	seen := map[string]bool{}
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" || seen[event] {
			continue
		}
		known := event == NotificationEventFinished
		for _, allowed := range NotificationEvents {
			if event == allowed {
				known = true
			}
		}
		if !known {
			return nil, invalidNotification("unknown event %q", event)
		}
		seen[event] = true
		out = append(out, event)
	}
	return out, nil
}

// notificationEventForState maps a terminal run state to the event it raises.
func notificationEventForState(state RunState) string {
	switch state {
	case RunSucceeded:
		return NotificationEventSucceeded
	case RunFailed, RunFailedActivation, RunRolledBack:
		return NotificationEventFailed
	case RunCancelled, RunSuperseded:
		return NotificationEventCancelled
	default:
		return ""
	}
}

// resolvedNotificationChannel is what validation produces: the sealed
// configuration, the plaintext column value and a display label safe to list.
type resolvedNotificationChannel struct {
	Config NotificationConfig
	URL    string
	Target string
}

// validateNotificationChannel checks one channel definition. `existing` is the
// current sealed configuration when editing, so an omitted credential keeps its
// stored value instead of being blanked by a form that never showed it.
func validateNotificationChannel(kind, rawURL string, cfg NotificationConfig, existing *NotificationConfig) (resolvedNotificationChannel, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = NotificationKindWebhook
	}
	if !validNotificationKind(kind) {
		return resolvedNotificationChannel{}, invalidNotification("unknown channel kind %q", kind)
	}
	rawURL = strings.TrimSpace(rawURL)
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	cfg.BotToken = strings.TrimSpace(cfg.BotToken)
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	cfg.SMTPHost = strings.TrimSpace(cfg.SMTPHost)
	cfg.SMTPUsername = strings.TrimSpace(cfg.SMTPUsername)
	cfg.SMTPSecurity = strings.ToLower(strings.TrimSpace(cfg.SMTPSecurity))
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	cfg.Topic = strings.TrimSpace(cfg.Topic)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.UserKey = strings.TrimSpace(cfg.UserKey)
	switch kind {
	case NotificationKindWebhook:
		if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
			return resolvedNotificationChannel{}, invalidNotification("notification URL must use http or https")
		}
		if _, err := url.Parse(rawURL); err != nil {
			return resolvedNotificationChannel{}, invalidNotification("notification URL is malformed")
		}
		return resolvedNotificationChannel{Config: NotificationConfig{}, URL: rawURL, Target: rawURL}, nil
	case NotificationKindDiscord, NotificationKindSlack, NotificationKindMattermost, NotificationKindTeams:
		if cfg.WebhookURL == "" && existing != nil {
			cfg.WebhookURL = existing.WebhookURL
		}
		if cfg.WebhookURL == "" && rawURL != "" {
			cfg.WebhookURL = rawURL
		}
		parsed, err := url.Parse(cfg.WebhookURL)
		// A self-hosted Mattermost on this machine may be reached over plain
		// HTTP on loopback, where the URL never crosses a network; every other
		// webhook URL is a credential and travels over TLS.
		secure := err == nil && parsed.Host != "" && (parsed.Scheme == "https" || (kind == NotificationKindMattermost && parsed.Scheme == "http" && loopbackAddress(parsed.Hostname())))
		if !secure {
			return resolvedNotificationChannel{}, invalidNotification("%s webhook URL must be an https address", kindName(kind))
		}
		host := strings.ToLower(parsed.Hostname())
		masked := maskWebhookURL(parsed)
		switch kind {
		case NotificationKindDiscord:
			validHost := host == "discord.com" || host == "discordapp.com" || strings.HasSuffix(host, ".discord.com") || strings.HasSuffix(host, ".discordapp.com")
			if !validHost || !strings.HasPrefix(parsed.Path, "/api/webhooks/") {
				return resolvedNotificationChannel{}, invalidNotification("Discord webhook URLs look like https://discord.com/api/webhooks/<id>/<token>")
			}
		case NotificationKindSlack:
			if host != "hooks.slack.com" || !strings.HasPrefix(parsed.Path, "/services/") {
				return resolvedNotificationChannel{}, invalidNotification("Slack incoming webhook URLs look like https://hooks.slack.com/services/…")
			}
		case NotificationKindMattermost:
			if parsed.User != nil || !mattermostHookRE.MatchString(strings.TrimRight(parsed.Path, "/")) {
				return resolvedNotificationChannel{}, invalidNotification("Mattermost incoming webhook URLs look like https://chat.example.com/hooks/<id>")
			}
		case NotificationKindTeams:
			if strings.HasSuffix(host, ".webhook.office.com") || host == "outlook.office.com" {
				return resolvedNotificationChannel{}, invalidNotification("Office 365 connector webhooks were retired in May 2026; create a Workflows webhook in Teams instead")
			}
			// A Workflows trigger is a Logic Apps or Power Platform address,
			// and its sig parameter is what authorizes the post.
			validHost := strings.HasSuffix(host, ".logic.azure.com") || strings.HasSuffix(host, ".powerplatform.com")
			if !validHost || !strings.Contains(parsed.Path, "/workflows/") || parsed.Query().Get("sig") == "" {
				return resolvedNotificationChannel{}, invalidNotification("Teams Workflows webhook URLs look like https://….logic.azure.com/workflows/<id>/triggers/manual/paths/invoke?…&sig=…")
			}
			// The path is long and says nothing the name does not; the
			// credential is in the query, which is never shown.
			masked = parsed.Scheme + "://" + host + "/workflows/••••"
		}
		return resolvedNotificationChannel{Config: NotificationConfig{WebhookURL: cfg.WebhookURL}, URL: masked, Target: masked}, nil
	case NotificationKindNtfy:
		if existing != nil {
			cfg.ServerURL = keep(cfg.ServerURL, existing.ServerURL)
			cfg.Topic = keep(cfg.Topic, existing.Topic)
			cfg.Token = keep(cfg.Token, existing.Token)
		}
		if cfg.ServerURL == "" {
			cfg.ServerURL = DefaultNtfyServer
		}
		if cfg.Token != "" && !ntfyTokenRE.MatchString(cfg.Token) {
			return resolvedNotificationChannel{}, invalidNotification("ntfy access tokens look like tk_ and 29 letters or digits")
		}
		server, err := notificationServer("ntfy", cfg.ServerURL, cfg.Token != "")
		if err != nil {
			return resolvedNotificationChannel{}, err
		}
		if !ntfyTopicRE.MatchString(cfg.Topic) {
			return resolvedNotificationChannel{}, invalidNotification("ntfy topic must be 1 to 64 letters, digits, dashes or underscores")
		}
		// The topic is masked like a webhook's token: on a public server,
		// whoever knows it reads every message.
		return resolvedNotificationChannel{
			Config: NotificationConfig{ServerURL: server, Topic: cfg.Topic, Token: cfg.Token},
			Target: server + "/••••",
		}, nil
	case NotificationKindGotify:
		if existing != nil {
			cfg.ServerURL = keep(cfg.ServerURL, existing.ServerURL)
			cfg.Token = keep(cfg.Token, existing.Token)
		}
		if cfg.ServerURL == "" {
			return resolvedNotificationChannel{}, invalidNotification("Gotify server URL is required")
		}
		server, err := notificationServer("Gotify", cfg.ServerURL, true)
		if err != nil {
			return resolvedNotificationChannel{}, err
		}
		if !gotifyTokenRE.MatchString(cfg.Token) {
			return resolvedNotificationChannel{}, invalidNotification("Gotify needs an application token from Apps → Create application")
		}
		return resolvedNotificationChannel{Config: NotificationConfig{ServerURL: server, Token: cfg.Token}, Target: server}, nil
	case NotificationKindPushover:
		if existing != nil {
			cfg.Token = keep(cfg.Token, existing.Token)
			cfg.UserKey = keep(cfg.UserKey, existing.UserKey)
		}
		if !pushoverKeyRE.MatchString(cfg.Token) {
			return resolvedNotificationChannel{}, invalidNotification("Pushover application tokens are 30 letters and digits")
		}
		if !pushoverKeyRE.MatchString(cfg.UserKey) {
			return resolvedNotificationChannel{}, invalidNotification("Pushover user and group keys are 30 letters and digits")
		}
		return resolvedNotificationChannel{
			Config: NotificationConfig{Token: cfg.Token, UserKey: cfg.UserKey},
			Target: "Pushover user " + cfg.UserKey[:3] + "••••",
		}, nil
	case NotificationKindTelegram:
		if cfg.BotToken == "" && existing != nil {
			cfg.BotToken = existing.BotToken
		}
		if !telegramTokenRE.MatchString(cfg.BotToken) {
			return resolvedNotificationChannel{}, invalidNotification("Telegram bot token must look like 123456789:AA…")
		}
		if !telegramChatRE.MatchString(cfg.ChatID) {
			return resolvedNotificationChannel{}, invalidNotification("Telegram chat id must be a numeric id or an @channel name")
		}
		return resolvedNotificationChannel{
			Config: NotificationConfig{BotToken: cfg.BotToken, ChatID: cfg.ChatID},
			URL:    "", Target: "Telegram chat " + cfg.ChatID,
		}, nil
	case NotificationKindEmail:
		if cfg.SMTPPassword == "" && existing != nil {
			cfg.SMTPPassword = existing.SMTPPassword
		}
		if cfg.SMTPSecurity == "" {
			cfg.SMTPSecurity = SMTPSecurityStartTLS
		}
		if cfg.SMTPSecurity != SMTPSecurityStartTLS && cfg.SMTPSecurity != SMTPSecurityTLS && cfg.SMTPSecurity != SMTPSecurityNone {
			return resolvedNotificationChannel{}, invalidNotification("SMTP security must be starttls, tls or none")
		}
		if cfg.SMTPPort == 0 {
			cfg.SMTPPort = map[string]int{SMTPSecurityStartTLS: 587, SMTPSecurityTLS: 465, SMTPSecurityNone: 25}[cfg.SMTPSecurity]
		}
		if cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
			return resolvedNotificationChannel{}, invalidNotification("SMTP port must be between 1 and 65535")
		}
		if cfg.SMTPHost == "" || len(cfg.SMTPHost) > 253 || (!smtpHostnameRE.MatchString(cfg.SMTPHost) && net.ParseIP(cfg.SMTPHost) == nil) {
			return resolvedNotificationChannel{}, invalidNotification("SMTP host must be a hostname or IP address")
		}
		if cfg.SMTPUsername != "" && cfg.SMTPSecurity == SMTPSecurityNone {
			return resolvedNotificationChannel{}, invalidNotification("SMTP authentication requires STARTTLS or TLS; a password must not travel in clear text")
		}
		from, err := mail.ParseAddress(cfg.From)
		if err != nil {
			return resolvedNotificationChannel{}, invalidNotification("sender address is invalid")
		}
		if len(cfg.To) == 0 || len(cfg.To) > maxNotificationRecipients {
			return resolvedNotificationChannel{}, invalidNotification("between 1 and %d recipient addresses are required", maxNotificationRecipients)
		}
		recipients := make([]string, 0, len(cfg.To))
		for _, raw := range cfg.To {
			address, err := mail.ParseAddress(strings.TrimSpace(raw))
			if err != nil {
				return resolvedNotificationChannel{}, invalidNotification("recipient %q is invalid", raw)
			}
			recipients = append(recipients, address.Address)
		}
		return resolvedNotificationChannel{
			Config: NotificationConfig{
				SMTPHost: cfg.SMTPHost, SMTPPort: cfg.SMTPPort, SMTPUsername: cfg.SMTPUsername,
				SMTPPassword: cfg.SMTPPassword, SMTPSecurity: cfg.SMTPSecurity, From: from.Address, To: recipients,
			},
			URL: "", Target: strings.Join(recipients, ", "),
		}, nil
	}
	return resolvedNotificationChannel{}, invalidNotification("unknown channel kind %q", kind)
}

// keep is a field left blank on edit: the stored value stays, because the form
// never showed it.
func keep(value, stored string) string {
	if value == "" {
		return stored
	}
	return value
}

// kindName is a kind as a sentence names it.
func kindName(kind string) string {
	switch kind {
	case NotificationKindTeams:
		return "Teams"
	case NotificationKindNtfy:
		return "ntfy"
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// loopbackAddress is an address that never leaves this machine. The backend
// shares the host's network, so a server published on 127.0.0.1 is reachable.
// It parses the address rather than matching a prefix as detection's
// loopbackHost does: 127.example.com is somebody else's machine.
func loopbackAddress(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// notificationServer checks a self-hosted server's base URL and returns it
// without a trailing slash. A server that is handed a token must be reached
// over TLS unless it is on loopback — the rule SMTP sign-in is held to — and
// a URL may not carry a sign-in of its own, which would be shown as the
// channel's target.
func notificationServer(product, raw string, credential bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", invalidNotification("%s server URL must be an http or https address", product)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalidNotification("%s server URL must be the server's address alone, without a sign-in, query or fragment", product)
	}
	if credential && parsed.Scheme == "http" && !loopbackAddress(parsed.Hostname()) {
		return "", invalidNotification("%s server URL must use https: a token must not travel in clear text (plain http is accepted on loopback only)", product)
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.EscapedPath(), "/"), nil
}

// maskWebhookURL keeps the host and every path segment but the last so the
// operator can tell channels apart, and hides the tail: on Discord and Slack
// the final segment is the credential.
func maskWebhookURL(parsed *url.URL) string {
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	visible := segments
	if len(segments) > 1 {
		visible = segments[:len(segments)-1]
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + strings.Join(visible, "/") + "/••••"
}

// NotificationEnvelope is the fact set every channel receives. Signed webhooks
// get it as JSON; provider kinds render it into a message. The first six fields
// are the historical contract and keep their names.
type NotificationEnvelope struct {
	Event         string    `json:"event"`
	RunID         int64     `json:"runId"`
	ProjectID     int64     `json:"projectId"`
	EnvironmentID int64     `json:"environmentId"`
	State         string    `json:"state"`
	SentAt        time.Time `json:"sentAt"`

	ProjectName     string `json:"projectName,omitempty"`
	EnvironmentName string `json:"environmentName,omitempty"`
	RunNumber       int64  `json:"runNumber,omitempty"`
	Operation       string `json:"operation,omitempty"`
	Trigger         string `json:"trigger,omitempty"`
	Actor           string `json:"actor,omitempty"`
	SourceRef       string `json:"sourceRef,omitempty"`
	SourceRevision  string `json:"sourceRevision,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	TerminalCode    string `json:"terminalCode,omitempty"`
	TerminalReason  string `json:"terminalReason,omitempty"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	URL             string `json:"url,omitempty"`
	// Alert carries a traffic alert's reading; it is set on the two traffic
	// events and on nothing else.
	Alert *TrafficAlertEnvelope `json:"alert,omitempty"`
	// Proxy carries a proxy alert's subject and reading; it is set on the two
	// proxy events and on nothing else.
	Proxy *ProxyAlertEnvelope `json:"proxy,omitempty"`
	// Database carries a database alert's subject and reading; it is set on
	// the two database events and on nothing else.
	Database *DatabaseAlertEnvelope `json:"database,omitempty"`
	// Host carries a host alert's subject and reading; it is set on the two
	// host events and on nothing else.
	Host *HostAlertEnvelope `json:"host,omitempty"`
}

// HostAlertEnvelope is what a host alert says when the machine itself is in
// trouble — its CPU or memory held high, a filesystem filling, a container
// gone down, a backup job failed — and when that is over. Server is the
// machine's name, because one channel often hears from several of them;
// Label names the subject in words and Detail is the reading against the
// rule's line.
type HostAlertEnvelope struct {
	RuleID int64  `json:"ruleId"`
	Kind   string `json:"kind"`
	// Subject is what the rule follows: "cpu", "memory", a mountpoint, a
	// container's name, or "job:<id>".
	Subject   string  `json:"subject"`
	Label     string  `json:"label"`
	Server    string  `json:"server,omitempty"`
	Detail    string  `json:"detail"`
	Threshold float64 `json:"threshold,omitempty"`
	// Since is when the subject entered the state being announced.
	Since time.Time `json:"since"`
	// Test marks a message sent from "Send test", so nobody acts on it.
	Test bool `json:"test,omitempty"`
}

// DatabaseAlertEnvelope is what a database alert says when a server stops
// answering, fills its disk, runs out of connections or falls behind, or a
// backup of it fails or is missed — and when that is over. Label names the
// database or the backup job in words and Detail is the reading against the
// rule's line, so the message can be acted on from a phone.
type DatabaseAlertEnvelope struct {
	RuleID int64  `json:"ruleId"`
	Kind   string `json:"kind"`
	// ConnectionID is the database a server reading is about; a backup job
	// may dump several, and names JobID instead.
	ConnectionID int64   `json:"connectionId,omitempty"`
	JobID        int64   `json:"jobId,omitempty"`
	Subject      string  `json:"subject"`
	Label        string  `json:"label"`
	Detail       string  `json:"detail"`
	Reading      float64 `json:"reading"`
	Threshold    float64 `json:"threshold,omitempty"`
	// Since is when the subject entered the state being announced.
	Since time.Time `json:"since"`
	// Test marks a message sent from "Send test", so nobody acts on it.
	Test bool `json:"test,omitempty"`
}

// ProxyAlertEnvelope is what a proxy alert says when a subject — a
// certificate, the engine, an upstream, a watched endpoint, a site — crosses
// a rule's line or comes back. Label names the subject in words and Detail is
// the reading, so a provider message can be acted on without opening the
// dashboard.
type ProxyAlertEnvelope struct {
	RuleID  int64  `json:"ruleId"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Level   string `json:"level"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	// Since is when the subject entered the state being announced.
	Since time.Time `json:"since"`
	// Test marks a message sent from "Send test", so nobody acts on it.
	Test bool `json:"test,omitempty"`
}

// TrafficAlertEnvelope is what a traffic alert says about itself when it
// fires or recovers: which rule, what it saw, against what limit, for how
// long. A webhook gets it verbatim; the providers get it as a sentence.
type TrafficAlertEnvelope struct {
	ID            int64   `json:"id"`
	Kind          string  `json:"kind"`
	Threshold     float64 `json:"threshold"`
	Observed      float64 `json:"observed"`
	WindowMinutes int     `json:"windowMinutes"`
	// Since is when the rule entered the state it is announcing.
	Since time.Time `json:"since"`
}

// notificationMessage is the rendered, provider-neutral form.
type notificationMessage struct {
	Emoji   string
	Title   string
	Summary string
	Fields  [][2]string
	URL     string
	// LinkLabel is the words an e-mail puts before URL; a run's own message
	// says "Open the run".
	LinkLabel string
	// Color is the Discord embed colour and Mattermost's attachment bar; a
	// Teams card reads it as attention or good, and ntfy, Gotify and Pushover
	// read the red as a reason to raise the message's priority. Slack and
	// e-mail ignore it.
	Color int
}

// The two colours a message's outcome is drawn in. Red is a failure or an
// alert firing: the one a phone should make noise for.
const (
	notificationRed   = 0xef4444
	notificationGreen = 0x22c55e
)

// urgent is a message that says something went wrong.
func (m notificationMessage) urgent() bool { return m.Color == notificationRed }

func renderNotification(envelope NotificationEnvelope) notificationMessage {
	project := envelope.ProjectName
	if project == "" {
		project = fmt.Sprintf("Deployment %d", envelope.ProjectID)
	}
	environment := envelope.EnvironmentName
	if environment == "" {
		environment = "production"
	}
	run := "Run"
	if envelope.RunNumber > 0 {
		run = fmt.Sprintf("Run #%d", envelope.RunNumber)
	} else if envelope.RunID > 0 {
		run = fmt.Sprintf("Run %d", envelope.RunID)
	}
	message := notificationMessage{URL: envelope.URL}
	switch envelope.Event {
	case NotificationEventTrafficFiring, NotificationEventTrafficRecovered:
		return renderTrafficAlert(envelope, project, environment)
	case NotificationEventProxyFiring, NotificationEventProxyRecovered:
		return renderProxyAlert(envelope)
	case NotificationEventDatabaseFiring, NotificationEventDatabaseRecovered:
		return renderDatabaseAlert(envelope)
	case NotificationEventHostFiring, NotificationEventHostRecovered:
		return renderHostAlert(envelope)
	case NotificationEventTest:
		message.Emoji, message.Color = "🔔", 0x5865f2
		message.Title = "Test notification from Just Dashboard"
		message.Summary = "This channel is configured correctly. Deployment events will arrive here."
		return message
	case NotificationEventStarted:
		message.Emoji, message.Color = "🚀", 0x3b82f6
		message.Title = fmt.Sprintf("%s · %s — %s started", project, environment, run)
		message.Summary = fmt.Sprintf("%s requested by %s.", humanizeOperation(envelope.Operation), describeTrigger(envelope))
	case NotificationEventSucceeded:
		message.Emoji, message.Color = "✅", notificationGreen
		message.Title = fmt.Sprintf("%s · %s — %s succeeded", project, environment, run)
		message.Summary = fmt.Sprintf("%s completed successfully.", humanizeOperation(envelope.Operation))
	case NotificationEventCancelled:
		message.Emoji, message.Color = "⏹", 0x6b7280
		message.Title = fmt.Sprintf("%s · %s — %s cancelled", project, environment, run)
		message.Summary = fmt.Sprintf("%s was cancelled before it finished.", humanizeOperation(envelope.Operation))
	default:
		message.Emoji, message.Color = "❌", notificationRed
		outcome := "failed"
		if envelope.State == string(RunRolledBack) {
			outcome = "failed and was rolled back"
		}
		message.Title = fmt.Sprintf("%s · %s — %s %s", project, environment, run, outcome)
		message.Summary = fmt.Sprintf("%s did not complete. The previous release keeps serving.", humanizeOperation(envelope.Operation))
	}
	fields := [][2]string{{"Operation", humanizeOperation(envelope.Operation)}, {"Trigger", describeTrigger(envelope)}}
	if envelope.SourceRef != "" || envelope.SourceRevision != "" {
		fields = append(fields, [2]string{"Source", describeSource(envelope)})
	}
	if envelope.DurationSeconds > 0 {
		fields = append(fields, [2]string{"Duration", humanDuration(time.Duration(envelope.DurationSeconds) * time.Second)})
	}
	if cause := namedCauseTitle(envelope.TerminalCode); cause != "" && envelope.Event == NotificationEventFailed {
		fields = append(fields, [2]string{"Cause", cause})
	}
	if envelope.TerminalReason != "" && envelope.Event == NotificationEventFailed {
		reason := envelope.TerminalReason
		if len(reason) > 600 {
			reason = reason[:600] + "…"
		}
		fields = append(fields, [2]string{"Reason", reason})
	} else if envelope.TerminalCode != "" && envelope.Event == NotificationEventFailed {
		fields = append(fields, [2]string{"Reason", envelope.TerminalCode})
	}
	if envelope.Endpoint != "" && envelope.Event == NotificationEventSucceeded {
		fields = append(fields, [2]string{"Address", envelope.Endpoint})
	}
	message.Fields = fields
	return message
}

// renderTrafficAlert is the sentence a traffic alert sends. The reading and
// the limit are both in it, because "5xx rate is high" is a message somebody
// has to open the dashboard to act on, and "5xx rate 4.2% over 5 min, limit
// 1%" is one they can act on from their phone.
func renderTrafficAlert(envelope NotificationEnvelope, project, environment string) notificationMessage {
	message := notificationMessage{URL: envelope.URL}
	alert := envelope.Alert
	if alert == nil {
		alert = &TrafficAlertEnvelope{}
	}
	window := fmt.Sprintf("%d min", alert.WindowMinutes)
	var reading, limit, what string
	switch alert.Kind {
	case TrafficAlertLatency:
		what = "slow"
		reading = fmt.Sprintf("p95 %s over %s", humanMillis(alert.Observed), window)
		limit = humanMillis(alert.Threshold)
	case TrafficAlertSilence:
		what = "silent"
		reading = fmt.Sprintf("no requests for %s", window)
		limit = "any traffic"
	default:
		what = "failing"
		reading = fmt.Sprintf("5xx rate %.1f%% over %s", alert.Observed, window)
		limit = fmt.Sprintf("%.1f%%", alert.Threshold)
	}
	if envelope.Event == NotificationEventTrafficFiring {
		message.Emoji, message.Color = "🚨", notificationRed
		message.Title = fmt.Sprintf("%s · %s is %s — %s", project, environment, what, reading)
		message.Summary = fmt.Sprintf("The limit is %s. Open the project's Logs page to see which requests.", limit)
	} else {
		message.Emoji, message.Color = "✅", notificationGreen
		message.Title = fmt.Sprintf("%s · %s recovered — %s", project, environment, reading)
		message.Summary = fmt.Sprintf("Back within the limit of %s.", limit)
	}
	fields := [][2]string{{"Reading", reading}, {"Limit", limit}, {"Window", window}}
	if !alert.Since.IsZero() {
		fields = append(fields, [2]string{"Since", alert.Since.UTC().Format("2006-01-02 15:04 UTC")})
	}
	message.Fields = fields
	return message
}

// proxyAlertWhat is each proxy alert kind as the noun phrase a title leads
// with. The kinds are the api package's; an unknown one still renders.
var proxyAlertWhat = map[string]string{
	"cert_expiring":     "Certificate expiring",
	"cert_expired":      "Certificate expired",
	"renewal_failed":    "Certbot renewal failed",
	"served_drift":      "Served certificate differs",
	"engine_down":       "Proxy engine not running",
	"upstream_down":     "Upstream down",
	"watch_unreachable": "Watched endpoint unreachable",
	"watch_untrusted":   "Watched endpoint untrusted",
	"watch_grade_below": "Watched TLS grade below minimum",
	"site_errors":       "Site failing",
}

// renderProxyAlert is the sentence a proxy alert sends: what, which subject,
// and the reading that decided it.
func renderProxyAlert(envelope NotificationEnvelope) notificationMessage {
	message := notificationMessage{URL: envelope.URL}
	alert := envelope.Proxy
	if alert == nil {
		alert = &ProxyAlertEnvelope{}
	}
	what := proxyAlertWhat[alert.Kind]
	if what == "" {
		what = "Proxy alert"
	}
	label := alert.Label
	if label == "" {
		label = alert.Subject
	}
	prefix := ""
	if alert.Test {
		prefix = "[test] "
	}
	if envelope.Event == NotificationEventProxyFiring {
		message.Emoji, message.Color = "🚨", notificationRed
		message.Title = fmt.Sprintf("%s%s — %s", prefix, what, label)
		message.Summary = alert.Detail
	} else {
		message.Emoji, message.Color = "✅", notificationGreen
		message.Title = fmt.Sprintf("%sResolved: %s — %s", prefix, what, label)
		message.Summary = alert.Detail
	}
	fields := [][2]string{{"Subject", label}}
	if alert.Detail != "" {
		fields = append(fields, [2]string{"Reading", alert.Detail})
	}
	if !alert.Since.IsZero() {
		fields = append(fields, [2]string{"Since", alert.Since.UTC().Format("2006-01-02 15:04 UTC")})
	}
	message.Fields = fields
	return message
}

// databaseAlertWhat is each database alert kind as the noun phrase a title
// leads with. The kinds are dbx's; an unknown one still renders.
var databaseAlertWhat = map[string]string{
	"down":          "Database not answering",
	"disk":          "Database disk filling up",
	"connections":   "Database connections near the limit",
	"replication":   "Replication falling behind",
	"backup_failed": "Backup failed",
	"backup_missed": "Backup missed",
}

// renderDatabaseAlert is the sentence a database alert sends: what, which
// database or backup job, and the reading against the line.
func renderDatabaseAlert(envelope NotificationEnvelope) notificationMessage {
	message := notificationMessage{URL: envelope.URL, LinkLabel: "Open it in Just Dashboard"}
	alert := envelope.Database
	if alert == nil {
		alert = &DatabaseAlertEnvelope{}
	}
	what := databaseAlertWhat[alert.Kind]
	if what == "" {
		what = "Database alert"
	}
	label := alert.Label
	if label == "" {
		label = alert.Subject
	}
	prefix := ""
	if alert.Test {
		prefix = "[test] "
	}
	if envelope.Event == NotificationEventDatabaseFiring {
		message.Emoji, message.Color = "🚨", notificationRed
		message.Title = fmt.Sprintf("%s%s — %s", prefix, what, label)
	} else {
		message.Emoji, message.Color = "✅", notificationGreen
		message.Title = fmt.Sprintf("%sResolved: %s — %s", prefix, what, label)
	}
	message.Summary = alert.Detail
	subject := "Database"
	if strings.HasPrefix(alert.Kind, "backup_") {
		subject = "Backup job"
	}
	message.Fields = [][2]string{{subject, label}}
	if !alert.Since.IsZero() {
		message.Fields = append(message.Fields, [2]string{"Since", alert.Since.UTC().Format("2006-01-02 15:04 UTC")})
	}
	return message
}

// hostAlertWhat is each host alert kind as the noun phrase a title leads
// with, and the word its subject goes by in the fields. The kinds are the api
// package's; an unknown one still renders.
var hostAlertWhat = map[string][2]string{
	"cpu_high":       {"CPU high", "Server"},
	"memory_high":    {"Memory high", "Server"},
	"disk_filling":   {"Disk filling up", "Filesystem"},
	"container_down": {"Container down", "Container"},
	"backup_failed":  {"Backup failed", "Backup job"},
}

// renderHostAlert is the sentence a host alert sends: what, which subject on
// which server, and the reading against the line.
func renderHostAlert(envelope NotificationEnvelope) notificationMessage {
	message := notificationMessage{URL: envelope.URL, LinkLabel: "Open it in Just Dashboard"}
	alert := envelope.Host
	if alert == nil {
		alert = &HostAlertEnvelope{}
	}
	words, ok := hostAlertWhat[alert.Kind]
	if !ok {
		words = [2]string{"Host alert", "Subject"}
	}
	label := alert.Label
	if label == "" {
		label = alert.Subject
	}
	prefix := ""
	if alert.Test {
		prefix = "[test] "
	}
	if envelope.Event == NotificationEventHostFiring {
		message.Emoji, message.Color = "🚨", notificationRed
		message.Title = fmt.Sprintf("%s%s — %s", prefix, words[0], label)
	} else {
		message.Emoji, message.Color = "✅", notificationGreen
		message.Title = fmt.Sprintf("%sResolved: %s — %s", prefix, words[0], label)
	}
	message.Summary = alert.Detail
	message.Fields = [][2]string{{words[1], label}}
	// The machine is named once: a CPU rule's subject already is the server.
	if alert.Server != "" && alert.Server != label {
		message.Fields = append(message.Fields, [2]string{"Server", alert.Server})
	}
	if !alert.Since.IsZero() {
		message.Fields = append(message.Fields, [2]string{"Since", alert.Since.UTC().Format("2006-01-02 15:04 UTC")})
	}
	return message
}

func humanMillis(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.2fs", ms/1000)
}

func humanizeOperation(operation string) string {
	switch Operation(operation) {
	case OperationDeploy:
		return "Deployment"
	case OperationRedeploy:
		return "Redeploy"
	case OperationForceBuild:
		return "Rebuild without cache"
	case OperationRollback:
		return "Rollback"
	case OperationRestart:
		return "Restart"
	case OperationStop:
		return "Stop"
	case OperationStart:
		return "Start"
	case OperationPreviewCreate:
		return "Preview deployment"
	case OperationPreviewRemove:
		return "Preview removal"
	case "":
		return "Deployment"
	}
	return strings.ToUpper(operation[:1]) + strings.ReplaceAll(operation[1:], "_", " ")
}

func describeTrigger(envelope NotificationEnvelope) string {
	trigger := strings.ReplaceAll(envelope.Trigger, "_", " ")
	if trigger == "" {
		trigger = "manual"
	}
	if envelope.Actor != "" && envelope.Actor != trigger {
		return trigger + " · " + envelope.Actor
	}
	return trigger
}

func describeSource(envelope NotificationEnvelope) string {
	revision := envelope.SourceRevision
	if len(revision) > 12 {
		revision = revision[:12]
	}
	switch {
	case envelope.SourceRef != "" && revision != "":
		return envelope.SourceRef + " @ " + revision
	case revision != "":
		return revision
	default:
		return envelope.SourceRef
	}
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// Provider payloads. Each one is the minimum the provider documents for a
// posted message; nothing about this server beyond the envelope is included.

// Discord refuses an embed whose title, description or field value exceeds
// its documented limits instead of truncating it, so a long project name would
// silently lose the whole notification.
const (
	discordTitleLimit       = 256
	discordDescriptionLimit = 4096
	discordFieldLimit       = 1024
)

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func discordPayload(message notificationMessage, sentAt time.Time) []byte {
	fields := make([]map[string]any, 0, len(message.Fields))
	for _, field := range message.Fields {
		value := truncateRunes(field[1], discordFieldLimit)
		fields = append(fields, map[string]any{"name": truncateRunes(field[0], discordTitleLimit), "value": value, "inline": len(value) < 40})
	}
	embed := map[string]any{
		"title": truncateRunes(message.Emoji+" "+message.Title, discordTitleLimit), "description": truncateRunes(message.Summary, discordDescriptionLimit),
		"color": message.Color, "fields": fields, "timestamp": sentAt.UTC().Format(time.RFC3339),
	}
	if message.URL != "" {
		embed["url"] = message.URL
	}
	return mustJSON(map[string]any{"embeds": []any{embed}})
}

// slackEscaper is the escaping Slack's mrkdwn documents: the three characters
// that would otherwise start a link, a mention or a channel reference. A
// project called "a<b" used to break the link that wrapped the title.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func slackPayload(message notificationMessage) []byte {
	var text strings.Builder
	escapedTitle := slackEscaper.Replace(message.Title)
	title := message.Emoji + " *" + escapedTitle + "*"
	if message.URL != "" {
		title = message.Emoji + " *<" + message.URL + "|" + escapedTitle + ">*"
	}
	text.WriteString(title)
	text.WriteString("\n")
	text.WriteString(slackEscaper.Replace(message.Summary))
	blocks := []any{map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text.String()}}}
	if len(message.Fields) > 0 {
		fields := make([]map[string]any, 0, len(message.Fields))
		for _, field := range message.Fields {
			fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*" + slackEscaper.Replace(field[0]) + "*\n" + slackEscaper.Replace(field[1])})
			if len(fields) == 10 {
				break
			}
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	return mustJSON(map[string]any{"text": message.Emoji + " " + escapedTitle, "blocks": blocks})
}

func telegramPayload(chatID string, message notificationMessage) []byte {
	var text strings.Builder
	if message.URL != "" {
		fmt.Fprintf(&text, "%s <b><a href=\"%s\">%s</a></b>\n", message.Emoji, html.EscapeString(message.URL), html.EscapeString(message.Title))
	} else {
		fmt.Fprintf(&text, "%s <b>%s</b>\n", message.Emoji, html.EscapeString(message.Title))
	}
	text.WriteString(html.EscapeString(message.Summary))
	for _, field := range message.Fields {
		fmt.Fprintf(&text, "\n<b>%s:</b> %s", html.EscapeString(field[0]), html.EscapeString(field[1]))
	}
	payload := map[string]any{"text": text.String(), "parse_mode": "HTML", "disable_web_page_preview": true}
	if strings.HasPrefix(chatID, "@") {
		payload["chat_id"] = chatID
	} else if id, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		payload["chat_id"] = id
	} else {
		payload["chat_id"] = chatID
	}
	return mustJSON(payload)
}

// linkLabel is the words a message puts before its link.
func linkLabel(message notificationMessage) string {
	if message.LinkLabel != "" {
		return message.LinkLabel
	}
	return "Open the run"
}

// truncateBytes cuts text to a byte limit on a character boundary, for the
// services whose limits are in bytes.
func truncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// plainNotificationText is a message as lines of text, for the push services
// that draw no markup: the summary, each field as "Name: value", and, where
// the service has nowhere else to put it, the link line an e-mail ends with.
func plainNotificationText(message notificationMessage, link bool) string {
	fields := make([]string, 0, len(message.Fields))
	for _, field := range message.Fields {
		fields = append(fields, field[0]+": "+field[1])
	}
	paragraphs := []string{}
	for _, paragraph := range []string{message.Summary, strings.Join(fields, "\n")} {
		if paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	if link && message.URL != "" {
		paragraphs = append(paragraphs, linkLabel(message)+": "+message.URL)
	}
	return strings.Join(paragraphs, "\n\n")
}

// ntfy's JSON publishing: the topic rides in the body and the server's root
// takes it. A message over 4096 bytes is turned into an attachment, so the
// text stays under that.
const ntfyMessageLimit = 4000

func ntfyPayload(topic string, message notificationMessage) []byte {
	priority := 3
	if message.urgent() {
		priority = 4
	}
	payload := map[string]any{
		"topic": topic, "title": message.Emoji + " " + message.Title,
		"message": truncateBytes(plainNotificationText(message, false), ntfyMessageLimit), "priority": priority,
	}
	if message.URL != "" {
		payload["click"] = message.URL
		payload["actions"] = []any{map[string]any{"action": "view", "label": linkLabel(message), "url": message.URL}}
	}
	return mustJSON(payload)
}

// Gotify's clients show a notification from priority 4 up and make noise from
// 8, so a failure or a firing alert is sent at 8 and everything else at 5.
// Its web client does not follow the click extra, so the link is in the text.
func gotifyPayload(message notificationMessage) []byte {
	priority := 5
	if message.urgent() {
		priority = 8
	}
	payload := map[string]any{"title": message.Emoji + " " + message.Title, "message": plainNotificationText(message, true), "priority": priority}
	if message.URL != "" {
		payload["extras"] = map[string]any{"client::notification": map[string]any{"click": map[string]any{"url": message.URL}}}
	}
	return mustJSON(payload)
}

// Pushover's documented limits, in characters.
const (
	pushoverMessageLimit  = 1024
	pushoverTitleLimit    = 250
	pushoverURLLimit      = 512
	pushoverURLTitleLimit = 100
)

// pushoverPayload sends a failure or a firing alert at priority 1, which
// sounds through the recipient's quiet hours, and everything else at 0.
func pushoverPayload(cfg NotificationConfig, message notificationMessage, sentAt time.Time) []byte {
	priority := 0
	if message.urgent() {
		priority = 1
	}
	payload := map[string]any{
		"token": cfg.Token, "user": cfg.UserKey,
		"title":    truncateRunes(message.Emoji+" "+message.Title, pushoverTitleLimit),
		"message":  truncateRunes(plainNotificationText(message, false), pushoverMessageLimit),
		"priority": priority, "timestamp": sentAt.Unix(),
	}
	if message.URL != "" && len(message.URL) <= pushoverURLLimit {
		payload["url"] = message.URL
		payload["url_title"] = truncateRunes(linkLabel(message), pushoverURLTitleLimit)
	}
	return mustJSON(payload)
}

// mattermostEscaper backslash-escapes the Markdown a Mattermost attachment
// would otherwise render, so a reason quoting build output cannot turn into
// emphasis, a link or a quote.
var mattermostEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "[", `\[`, "]", `\]`, "#", `\#`, "|", `\|`, ">", `\>`)

// mattermostPayload is Slack's attachment shape, which Mattermost documents
// for incoming webhooks: the outcome's colour as the bar, the title linked to
// the run, the summary and the fields side by side.
func mattermostPayload(message notificationMessage) []byte {
	fields := make([]map[string]any, 0, len(message.Fields))
	for _, field := range message.Fields {
		fields = append(fields, map[string]any{"short": len(field[1]) < 40, "title": field[0], "value": mattermostEscaper.Replace(field[1])})
	}
	attachment := map[string]any{
		"fallback": message.Emoji + " " + message.Title, "color": fmt.Sprintf("#%06x", message.Color),
		"title": message.Emoji + " " + message.Title, "text": mattermostEscaper.Replace(message.Summary), "fields": fields,
	}
	if message.URL != "" {
		attachment["title_link"] = message.URL
	}
	return mustJSON(map[string]any{"attachments": []any{attachment}})
}

// teamsPayload is the Adaptive Card a Workflows "post to a channel when a
// webhook request is received" trigger takes, wrapped as a message with one
// attachment: the title in the outcome's colour, the summary, the fields as
// facts, and the link as the card's one action.
func teamsPayload(message notificationMessage) []byte {
	color := "default"
	switch message.Color {
	case notificationRed:
		color = "attention"
	case notificationGreen:
		color = "good"
	}
	body := []any{map[string]any{"type": "TextBlock", "text": message.Emoji + " " + message.Title, "weight": "bolder", "size": "medium", "wrap": true, "color": color}}
	if message.Summary != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": message.Summary, "wrap": true})
	}
	if len(message.Fields) > 0 {
		facts := make([]map[string]any, 0, len(message.Fields))
		for _, field := range message.Fields {
			facts = append(facts, map[string]any{"title": field[0], "value": field[1]})
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4",
		"body": body, "msteams": map[string]any{"width": "Full"},
	}
	if message.URL != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": linkLabel(message), "url": message.URL}}
	}
	return mustJSON(map[string]any{"type": "message", "attachments": []any{map[string]any{
		"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": card,
	}}})
}

func emailMessage(cfg NotificationConfig, message notificationMessage, sentAt time.Time) (subject string, body []byte) {
	subject = message.Emoji + " " + message.Title
	var text strings.Builder
	text.WriteString(message.Title)
	text.WriteString("\r\n\r\n")
	text.WriteString(message.Summary)
	text.WriteString("\r\n")
	for _, field := range message.Fields {
		fmt.Fprintf(&text, "\r\n%s: %s", field[0], field[1])
	}
	if message.URL != "" {
		fmt.Fprintf(&text, "\r\n\r\n%s: %s", linkLabel(message), message.URL)
	}
	text.WriteString("\r\n\r\n— Just Dashboard\r\n")
	var out bytes.Buffer
	fmt.Fprintf(&out, "From: %s\r\n", cfg.From)
	fmt.Fprintf(&out, "To: %s\r\n", strings.Join(cfg.To, ", "))
	fmt.Fprintf(&out, "Subject: %s\r\n", encodeSubject(subject))
	fmt.Fprintf(&out, "Date: %s\r\n", sentAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&out, "Message-ID: <%d.%d@just-dashboard>\r\n", sentAt.UnixNano(), len(message.Title))
	out.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\nX-Mailer: Just Dashboard\r\n\r\n")
	out.WriteString(text.String())
	return subject, out.Bytes()
}

// encodeSubject uses RFC 2047 encoding so the emoji survive every relay.
func encodeSubject(subject string) string {
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
}

// Mailer delivers one rendered message. It is a function so tests can capture
// what would have been sent without a listening SMTP server.
type Mailer func(ctx context.Context, cfg NotificationConfig, body []byte) error

// smtpDialTimeout bounds the connection and every SMTP exchange.
const smtpDialTimeout = 15 * time.Second

// sendSMTPMail speaks SMTP with the configured security. PLAIN authentication
// is only ever offered over TLS; validation refuses a username without it.
func sendSMTPMail(ctx context.Context, cfg NotificationConfig, body []byte) error {
	address := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	var conn net.Conn
	var err error
	tlsConfig := &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}
	if cfg.SMTPSecurity == SMTPSecurityTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(smtpDialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if cfg.SMTPSecurity == SMTPSecurityStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp server %s does not offer STARTTLS", cfg.SMTPHost)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if cfg.SMTPUsername != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, recipient := range cfg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// NotificationDispatcher turns engine run transitions into channel deliveries.
// It is the RunObserver that makes failed and cancelled runs reach the same
// audience as successful ones.
type NotificationDispatcher struct {
	store   *AutomationStore
	runs    *OrchestrationStore
	baseURL func() string
	client  *http.Client
	log     *slog.Logger
}

func NewNotificationDispatcher(store *AutomationStore, runs *OrchestrationStore, baseURL func() string, log *slog.Logger) *NotificationDispatcher {
	return &NotificationDispatcher{store: store, runs: runs, baseURL: baseURL, log: log}
}

// WithHTTPClient replaces the delivery client, for tests.
func (d *NotificationDispatcher) WithHTTPClient(client *http.Client) *NotificationDispatcher {
	d.client = client
	return d
}

func (d *NotificationDispatcher) RunStarted(ctx context.Context, run EngineRun) {
	d.dispatch(ctx, run, NotificationEventStarted)
}

func (d *NotificationDispatcher) RunFinished(ctx context.Context, run EngineRun) {
	event := notificationEventForState(run.State)
	if event == "" {
		return
	}
	d.dispatch(ctx, run, event)
}

func (d *NotificationDispatcher) dispatch(ctx context.Context, run EngineRun, event string) {
	if d == nil || d.store == nil {
		return
	}
	channels, err := d.store.ListNotificationChannels(ctx)
	if err != nil {
		d.warn(run.ID, "notification channels unavailable", err)
		return
	}
	var envelope *NotificationEnvelope
	for _, channel := range channels {
		if !channel.Enabled || !notificationEventSelected(channel.Events, event) {
			continue
		}
		delivered, err := d.store.NotificationDelivered(ctx, channel.ID, run.ID, event)
		if err != nil {
			d.warn(run.ID, "notification history unavailable", err)
			continue
		}
		if delivered {
			continue
		}
		if envelope == nil {
			built := d.envelope(ctx, run, event)
			envelope = &built
		}
		if err := d.store.DeliverNotification(ctx, d.client, channel.ID, *envelope); err != nil {
			d.warn(run.ID, "notification delivery failed for channel "+channel.Name, err)
		}
	}
}

func (d *NotificationDispatcher) warn(runID int64, message string, err error) {
	if d.log != nil {
		d.log.Warn(message, "run", runID, "err", err)
	}
}

// envelope assembles the facts a channel may state. Every lookup is optional:
// a notification with fewer details still beats silence.
func (d *NotificationDispatcher) envelope(ctx context.Context, run EngineRun, event string) NotificationEnvelope {
	envelope := NotificationEnvelope{
		Event: event, RunID: run.ID, ProjectID: run.ProjectID, EnvironmentID: run.EnvironmentID,
		State: string(run.State), SentAt: time.Now().UTC(), RunNumber: run.RunNumber,
		Operation: string(run.Operation), Trigger: string(run.Trigger), Actor: run.Actor,
		SourceRevision: run.SourceRevision, TerminalCode: run.TerminalCode, TerminalReason: run.TerminalReason,
	}
	if run.EndedAt != nil {
		started := run.RequestedAt
		if run.ClaimedAt != nil {
			started = *run.ClaimedAt
		}
		if seconds := int64(run.EndedAt.Sub(started).Seconds()); seconds > 0 {
			envelope.DurationSeconds = seconds
		}
	}
	if d.runs != nil {
		if details, err := d.runs.RunNotificationContext(ctx, run); err == nil {
			envelope.ProjectName = details.ProjectName
			envelope.EnvironmentName = details.EnvironmentName
			envelope.Endpoint = details.Endpoint
			if envelope.SourceRef == "" {
				envelope.SourceRef = details.SourceRef
			}
			if envelope.SourceRevision == "" {
				envelope.SourceRevision = details.SourceRevision
			}
		}
	}
	if d.baseURL != nil {
		if base := strings.TrimRight(strings.TrimSpace(d.baseURL()), "/"); base != "" {
			envelope.URL = fmt.Sprintf("%s/deploy/%d/runs/%d", base, run.ProjectID, run.ID)
		}
	}
	return envelope
}

// RunNotificationContext is the human context around a run: names, source and
// address. It is read once per event, never per channel.
type RunNotificationContext struct {
	ProjectName     string
	EnvironmentName string
	Endpoint        string
	SourceRef       string
	SourceRevision  string
}

func (s *OrchestrationStore) RunNotificationContext(ctx context.Context, run EngineRun) (RunNotificationContext, error) {
	var details RunNotificationContext
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(archived_name, ''), name) FROM deploy_projects WHERE id = ?`, run.ProjectID).
		Scan(&details.ProjectName); err != nil {
		return details, err
	}
	if run.EnvironmentID == 0 {
		return details, nil
	}
	var identity string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.name,
		       COALESCE((SELECT d.resource_id FROM deploy_dependencies d
		                  WHERE d.environment_id = e.id AND d.kind = 'domain' ORDER BY d.id LIMIT 1), ''),
		       COALESCE((SELECT src.identity_json FROM deploy_sources src
		                  WHERE src.environment_id = e.id AND src.revision = ? ORDER BY src.id DESC LIMIT 1), '{}')
		  FROM deploy_environments e WHERE e.id = ?`, run.PlanRevision, run.EnvironmentID).
		Scan(&details.EnvironmentName, &details.Endpoint, &identity)
	if err != nil {
		return details, err
	}
	var source SourceIdentity
	if json.Unmarshal([]byte(identity), &source) == nil {
		details.SourceRef = source.Ref
		details.SourceRevision = source.Revision
	}
	return details, nil
}

// NotificationDelivered reports whether a channel already received this event
// for this run. Observers can fire more than once for one transition (a
// reclaimed lease, a restart), and one message per outcome is the contract.
func (s *AutomationStore) NotificationDelivered(ctx context.Context, channelID, runID int64, event string) (bool, error) {
	var count int
	now := s.now().UTC()
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM deploy_notification_deliveries WHERE channel_id=? AND run_id=? AND event=? AND (status='delivered' OR (status='pending' AND created_at>?) OR (status='failed' AND next_attempt_at>?))`,
		channelID, runID, event, now.Add(-notificationReservationTTL).Unix(), now.Unix()).Scan(&count)
	return count > 0, err
}

// notificationReservationTTL bounds how long a reserved attempt blocks others.
// A process that dies mid-send leaves a pending row; after this long the row
// is treated as failed and the event may be attempted again.
const notificationReservationTTL = 5 * time.Minute

// Retry policy for failed deliveries. Three attempts, spaced so that a provider
// blip and a short outage both recover without a human, while a dead endpoint
// stops costing anything after half an hour.
const notificationMaxAttempts = 3

var notificationRetryDelays = []time.Duration{time.Minute, 5 * time.Minute}

// RetryFailedDeliveries re-sends failed run deliveries whose retry time has
// come. Test deliveries (run 0) are never retried: the operator is watching.
func (d *NotificationDispatcher) RetryFailedDeliveries(ctx context.Context) {
	if d == nil || d.store == nil || d.runs == nil {
		return
	}
	due, err := d.store.dueNotificationRetries(ctx, 50)
	if err != nil {
		d.warn(0, "notification retry lookup failed", err)
		return
	}
	for _, item := range due {
		run, err := d.runs.Run(ctx, item.RunID)
		if err != nil {
			_ = d.store.abandonNotificationRetry(ctx, item)
			continue
		}
		if err := d.store.DeliverNotification(ctx, d.client, item.ChannelID, d.envelope(ctx, *run, item.Event)); err != nil && !errors.Is(err, ErrHookDisabled) {
			d.warn(run.ID, "notification retry failed", err)
		}
	}
}

// discardBody drains and discards a provider response so the connection can be
// reused; bodies are never persisted.
func discardBody(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

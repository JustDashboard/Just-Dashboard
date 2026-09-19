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
)

// NotificationEvents is the closed vocabulary a channel may select from.
var NotificationEvents = []string{
	NotificationEventStarted, NotificationEventSucceeded, NotificationEventFailed, NotificationEventCancelled,
}

// Notification channel kinds. The webhook kind is the signed generic contract
// the product always had; the rest deliver a rendered message to a provider.
const (
	NotificationKindWebhook  = "webhook"
	NotificationKindDiscord  = "discord"
	NotificationKindSlack    = "slack"
	NotificationKindTelegram = "telegram"
	NotificationKindEmail    = "email"
)

var NotificationKinds = []string{
	NotificationKindWebhook, NotificationKindDiscord, NotificationKindSlack, NotificationKindTelegram, NotificationKindEmail,
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
)

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
	switch kind {
	case NotificationKindWebhook:
		if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
			return resolvedNotificationChannel{}, invalidNotification("notification URL must use http or https")
		}
		if _, err := url.Parse(rawURL); err != nil {
			return resolvedNotificationChannel{}, invalidNotification("notification URL is malformed")
		}
		return resolvedNotificationChannel{Config: NotificationConfig{}, URL: rawURL, Target: rawURL}, nil
	case NotificationKindDiscord, NotificationKindSlack:
		if cfg.WebhookURL == "" && existing != nil {
			cfg.WebhookURL = existing.WebhookURL
		}
		if cfg.WebhookURL == "" && rawURL != "" {
			cfg.WebhookURL = rawURL
		}
		parsed, err := url.Parse(cfg.WebhookURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return resolvedNotificationChannel{}, invalidNotification("%s webhook URL must be an https address", kind)
		}
		host := strings.ToLower(parsed.Hostname())
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
		}
		masked := maskWebhookURL(parsed)
		return resolvedNotificationChannel{Config: NotificationConfig{WebhookURL: cfg.WebhookURL}, URL: masked, Target: masked}, nil
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
	// Color is the Discord embed colour; Slack and e-mail ignore it.
	Color int
}

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
		message.Emoji, message.Color = "✅", 0x22c55e
		message.Title = fmt.Sprintf("%s · %s — %s succeeded", project, environment, run)
		message.Summary = fmt.Sprintf("%s completed successfully.", humanizeOperation(envelope.Operation))
	case NotificationEventCancelled:
		message.Emoji, message.Color = "⏹", 0x6b7280
		message.Title = fmt.Sprintf("%s · %s — %s cancelled", project, environment, run)
		message.Summary = fmt.Sprintf("%s was cancelled before it finished.", humanizeOperation(envelope.Operation))
	default:
		message.Emoji, message.Color = "❌", 0xef4444
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
		message.Emoji, message.Color = "🚨", 0xef4444
		message.Title = fmt.Sprintf("%s · %s is %s — %s", project, environment, what, reading)
		message.Summary = fmt.Sprintf("The limit is %s. Open the project's Logs page to see which requests.", limit)
	} else {
		message.Emoji, message.Color = "✅", 0x22c55e
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
		fmt.Fprintf(&text, "\r\n\r\nOpen the run: %s", message.URL)
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

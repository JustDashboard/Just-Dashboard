package logdrain

import (
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Record is one thing a deployment said: a line of a container's output, or
// a request the ingress served it.
type Record struct {
	At          time.Time
	ProjectID   int64
	Project     string
	Environment string
	Stream      string
	// Output.
	ContainerID   string
	ContainerName string
	IOStream      string
	Message       string
	// Requests.
	Request *Request
}

// Request is the request-log fields a drain carries.
type Request struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Query      string  `json:"query,omitempty"`
	Host       string  `json:"host,omitempty"`
	Proto      string  `json:"proto,omitempty"`
	Status     int     `json:"status"`
	Size       int64   `json:"size"`
	DurationMs float64 `json:"durationMs,omitempty"`
	Timed      bool    `json:"-"`
	RemoteIP   string  `json:"remoteIp,omitempty"`
	UserAgent  string  `json:"userAgent,omitempty"`
	Referer    string  `json:"referer,omitempty"`
}

// Level is the record's severity in the words every log service shares:
// error, warn, info, debug, or "" when nothing says. A request's is its
// status class. A line's is what the line says of itself — a JSON or logfmt
// level field, or a leading level word — and nothing else: stderr is not an
// error stream, and a version notice printed there is not an error.
func (r Record) Level() string {
	if r.Request != nil {
		switch {
		case r.Request.Status >= 500:
			return "error"
		case r.Request.Status >= 400:
			return "warn"
		default:
			return "info"
		}
	}
	return lineLevel(r.Message)
}

var (
	logfmtLevelRe = regexp.MustCompile(`(?i)(?:^|\s)(?:level|lvl|severity)=["']?([a-z]+)`)
	leadingLevel  = regexp.MustCompile(`(?i)^\s*(?:\d{4}-\d\d-\d\d[T ][0-9:.,]+Z?\s+)?[\[(<]?(fatal|panic|critical|crit|error|err|warning|warn|info|notice|debug|trace)[\])>:]?\s`)
)

func lineLevel(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]any
		if json.Unmarshal([]byte(trimmed), &fields) == nil {
			for _, key := range []string{"level", "severity", "lvl", "log.level", "levelname"} {
				if value, ok := fields[key].(string); ok {
					return normalizeLevel(value)
				}
				// pino and bunyan write numbers: 10 trace … 60 fatal.
				if value, ok := fields[key].(float64); ok {
					switch {
					case value >= 50:
						return "error"
					case value >= 40:
						return "warn"
					case value >= 30:
						return "info"
					default:
						return "debug"
					}
				}
			}
			return ""
		}
	}
	if match := logfmtLevelRe.FindStringSubmatch(trimmed); match != nil {
		return normalizeLevel(match[1])
	}
	if match := leadingLevel.FindStringSubmatch(trimmed + " "); match != nil {
		return normalizeLevel(match[1])
	}
	return ""
}

func normalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "fatal", "panic", "critical", "crit", "error", "err", "alert", "emerg", "emergency":
		return "error"
	case "warning", "warn":
		return "warn"
	case "info", "notice", "information":
		return "info"
	case "debug", "trace", "verbose":
		return "debug"
	}
	return ""
}

// body is the record's one line: the output line itself, or a request as a
// person reads it in a terminal.
func (r Record) body() string {
	if r.Request == nil {
		return r.Message
	}
	target := r.Request.Path
	if r.Request.Query != "" {
		target += "?" + r.Request.Query
	}
	line := r.Request.Method + " " + target + " " + strconv.Itoa(r.Request.Status)
	if r.Request.Timed {
		// Two places are what a person reads; the attribute keeps them all.
		line += " " + strconv.FormatFloat(math.Round(r.Request.DurationMs*100)/100, 'f', -1, 64) + "ms"
	}
	return line
}

// Encoded is a batch on the wire.
type Encoded struct {
	Body        []byte
	ContentType string
}

// endpointFor finishes a base address the way each service's own clients
// do: OpenTelemetry exporters append /v1/logs to OTEL_EXPORTER_OTLP_ENDPOINT
// (so Grafana Cloud's ".../otlp" works as given), and Loki's push path goes
// after a base address or a reverse proxy's prefix. An address that already
// ends in the path is used as it is.
func endpointFor(kind, endpoint string) string {
	suffix := map[string]string{KindOTLP: "/v1/logs", KindLoki: "/loki/api/v1/push"}[kind]
	if suffix == "" {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, suffix) {
		return endpoint
	}
	parsed.Path = path + suffix
	return parsed.String()
}

// encode renders a batch in the drain's format. hostName is this server's,
// sent as each resource's host.name (or host label) so one service can hold
// several servers' records.
func encode(kind string, records []Record, hostName string) (Encoded, error) {
	switch kind {
	case KindOTLP:
		return encodeOTLP(records, hostName)
	case KindLoki:
		return encodeLoki(records, hostName)
	default:
		return encodeLines(records, hostName)
	}
}

// OTLP/HTTP with the JSON encoding of the logs proto: one resource per
// project and environment, named the way OpenTelemetry's semantic
// conventions name a service, and request fields under their HTTP names.
type otlpValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

func stringAttribute(key, value string) otlpAttribute {
	return otlpAttribute{Key: key, Value: otlpValue{StringValue: &value}}
}

func intAttribute(key string, value int64) otlpAttribute {
	text := strconv.FormatInt(value, 10)
	return otlpAttribute{Key: key, Value: otlpValue{IntValue: &text}}
}

func doubleAttribute(key string, value float64) otlpAttribute {
	return otlpAttribute{Key: key, Value: otlpValue{DoubleValue: &value}}
}

type otlpLogRecord struct {
	TimeUnixNano         string          `json:"timeUnixNano"`
	ObservedTimeUnixNano string          `json:"observedTimeUnixNano"`
	SeverityNumber       int             `json:"severityNumber,omitempty"`
	SeverityText         string          `json:"severityText,omitempty"`
	Body                 otlpValue       `json:"body"`
	Attributes           []otlpAttribute `json:"attributes"`
}

var otlpSeverity = map[string]int{"debug": 5, "info": 9, "warn": 13, "error": 17}

func encodeOTLP(records []Record, hostName string) (Encoded, error) {
	type resourceKey struct {
		project     string
		environment string
		projectID   int64
	}
	order := []resourceKey{}
	grouped := map[resourceKey][]otlpLogRecord{}
	observed := strconv.FormatInt(time.Now().UnixNano(), 10)
	for _, record := range records {
		key := resourceKey{record.Project, record.Environment, record.ProjectID}
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		body := record.body()
		level := record.Level()
		attributes := []otlpAttribute{stringAttribute("just_dashboard.stream", record.Stream)}
		if record.Request == nil {
			attributes = append(attributes, stringAttribute("log.iostream", record.IOStream))
			if record.ContainerID != "" {
				attributes = append(attributes, stringAttribute("container.id", record.ContainerID))
			}
			if record.ContainerName != "" {
				attributes = append(attributes, stringAttribute("container.name", record.ContainerName))
			}
		} else {
			request := record.Request
			attributes = append(attributes,
				stringAttribute("http.request.method", request.Method),
				stringAttribute("url.path", request.Path),
				intAttribute("http.response.status_code", int64(request.Status)),
				intAttribute("http.response.body.size", request.Size))
			if request.Query != "" {
				attributes = append(attributes, stringAttribute("url.query", request.Query))
			}
			if request.Host != "" {
				attributes = append(attributes, stringAttribute("server.address", request.Host))
			}
			if name, version, ok := strings.Cut(request.Proto, "/"); ok {
				// "HTTP/1.1" is the convention's name "http" and version "1.1".
				attributes = append(attributes, stringAttribute("network.protocol.name", strings.ToLower(name)),
					stringAttribute("network.protocol.version", version))
			}
			if request.RemoteIP != "" {
				attributes = append(attributes, stringAttribute("client.address", request.RemoteIP))
			}
			if request.UserAgent != "" {
				attributes = append(attributes, stringAttribute("user_agent.original", request.UserAgent))
			}
			if request.Referer != "" {
				attributes = append(attributes, stringAttribute("http.request.header.referer", request.Referer))
			}
			if request.Timed {
				attributes = append(attributes, doubleAttribute("http.server.request.duration", request.DurationMs/1000))
			}
		}
		grouped[key] = append(grouped[key], otlpLogRecord{
			TimeUnixNano: strconv.FormatInt(record.At.UnixNano(), 10), ObservedTimeUnixNano: observed,
			SeverityNumber: otlpSeverity[level], SeverityText: strings.ToUpper(level),
			Body: otlpValue{StringValue: &body}, Attributes: attributes,
		})
	}
	type scopeLogs struct {
		Scope      map[string]string `json:"scope"`
		LogRecords []otlpLogRecord   `json:"logRecords"`
	}
	type resourceLogs struct {
		Resource  map[string][]otlpAttribute `json:"resource"`
		ScopeLogs []scopeLogs                `json:"scopeLogs"`
	}
	payload := struct {
		ResourceLogs []resourceLogs `json:"resourceLogs"`
	}{}
	for _, key := range order {
		resource := []otlpAttribute{
			stringAttribute("service.name", key.project),
			stringAttribute("service.namespace", "just-dashboard"),
			stringAttribute("deployment.environment.name", key.environment),
			intAttribute("just_dashboard.project_id", key.projectID),
		}
		if hostName != "" {
			resource = append(resource, stringAttribute("host.name", hostName))
		}
		payload.ResourceLogs = append(payload.ResourceLogs, resourceLogs{
			Resource:  map[string][]otlpAttribute{"attributes": resource},
			ScopeLogs: []scopeLogs{{Scope: map[string]string{"name": "just-dashboard"}, LogRecords: grouped[key]}},
		})
	}
	body, err := json.Marshal(payload)
	return Encoded{Body: body, ContentType: "application/json"}, err
}

// Loki's push API: one stream per project, environment, stream and level,
// labels kept to those few so a stream's cardinality is the deployments',
// not their requests'. A request's line is its JSON, which LogQL's json
// parser reads field by field.
func encodeLoki(records []Record, hostName string) (Encoded, error) {
	type entry struct {
		at   int64
		line string
	}
	type stream struct {
		Stream  map[string]string `json:"stream"`
		Values  [][2]string       `json:"values"`
		entries []entry
	}
	order := []string{}
	streams := map[string]*stream{}
	for _, record := range records {
		labels := map[string]string{
			"service_name": record.Project,
			"environment":  record.Environment,
			"stream":       record.Stream,
			"source":       "just-dashboard",
		}
		if hostName != "" {
			labels["host"] = hostName
		}
		if level := record.Level(); level != "" {
			labels["level"] = level
		}
		line := record.Message
		if record.Request != nil {
			encoded, err := json.Marshal(record.Request)
			if err != nil {
				return Encoded{}, err
			}
			line = string(encoded)
		} else if record.IOStream != "" {
			labels["iostream"] = record.IOStream
		}
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var id strings.Builder
		for _, key := range keys {
			id.WriteString(key + "=" + labels[key] + "\x00")
		}
		existing, ok := streams[id.String()]
		if !ok {
			existing = &stream{Stream: labels}
			streams[id.String()] = existing
			order = append(order, id.String())
		}
		existing.entries = append(existing.entries, entry{record.At.UnixNano(), line})
	}
	payload := struct {
		Streams []*stream `json:"streams"`
	}{}
	for _, id := range order {
		// Loki rejects entries older than the newest one already in a
		// stream unless out-of-order writes are on; sending each stream in
		// time order costs nothing either way.
		current := streams[id]
		sort.SliceStable(current.entries, func(i, j int) bool { return current.entries[i].at < current.entries[j].at })
		for _, e := range current.entries {
			current.Values = append(current.Values, [2]string{strconv.FormatInt(e.at, 10), e.line})
		}
		payload.Streams = append(payload.Streams, current)
	}
	body, err := json.Marshal(payload)
	return Encoded{Body: body, ContentType: "application/json"}, err
}

// JSON lines: one object per record, flat enough for any pipeline to index
// without a mapping.
func encodeLines(records []Record, hostName string) (Encoded, error) {
	var buffer strings.Builder
	for _, record := range records {
		line := map[string]any{
			"timestamp":   record.At.UTC().Format(time.RFC3339Nano),
			"service":     record.Project,
			"project_id":  record.ProjectID,
			"environment": record.Environment,
			"stream":      record.Stream,
			"message":     record.body(),
		}
		if hostName != "" {
			line["host"] = hostName
		}
		if level := record.Level(); level != "" {
			line["level"] = level
		}
		if record.Request == nil {
			line["iostream"] = record.IOStream
			if record.ContainerID != "" {
				line["container_id"] = record.ContainerID
			}
			if record.ContainerName != "" {
				line["container_name"] = record.ContainerName
			}
		} else {
			line["request"] = record.Request
		}
		encoded, err := json.Marshal(line)
		if err != nil {
			return Encoded{}, err
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}
	return Encoded{Body: []byte(buffer.String()), ContentType: "application/x-ndjson"}, nil
}

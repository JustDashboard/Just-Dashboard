package dnsservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

var (
	errQueryHistoryShape = errors.New("native query history contains an unreadable entry inventory; no complete history is reported")
	errQueryHistoryBound = errors.New("native query history exceeds its requested bound")
)

func (c *nativeClient) queryHistory(raw json.RawMessage) ([]Query, error) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return nil, errQueryHistoryShape
	}
	if len(entries) > 100 {
		return nil, errQueryHistoryBound
	}
	queries := make([]Query, 0, len(entries))
	for _, entry := range entries {
		query, valid := c.queryEntry(entry)
		if !valid {
			return nil, errQueryHistoryShape
		}
		queries = append(queries, Query{c.text(query.At), c.text(query.Client), c.text(query.Name), c.text(query.Type), c.text(query.Status), c.text(query.Protocol)})
	}
	return queries, nil
}

func (c *nativeClient) queryEntry(raw json.RawMessage) (Query, bool) {
	switch c.engine {
	case AdGuard:
		var row *struct {
			Time     *string         `json:"time"`
			Client   *string         `json:"client"`
			Reason   *string         `json:"reason"`
			Protocol json.RawMessage `json:"client_proto"`
			Question *struct {
				Name *string `json:"name"`
				Type *string `json:"type"`
			} `json:"question"`
		}
		if json.Unmarshal(raw, &row) != nil || row == nil || row.Time == nil || row.Client == nil || row.Reason == nil || row.Question == nil || row.Question.Name == nil || row.Question.Type == nil {
			return Query{}, false
		}
		protocol := ""
		if len(row.Protocol) != 0 {
			var valid bool
			protocol, valid = queryString(row.Protocol, false)
			if !valid {
				return Query{}, false
			}
		}
		return Query{*row.Time, *row.Client, *row.Question.Name, *row.Question.Type, *row.Reason, protocol}, true
	case PiHole:
		var row *struct {
			Time   *float64        `json:"time"`
			Type   *string         `json:"type"`
			Domain *string         `json:"domain"`
			Status json.RawMessage `json:"status"`
			Client *struct {
				IP *string `json:"ip"`
			} `json:"client"`
		}
		if json.Unmarshal(raw, &row) != nil || row == nil || row.Time == nil || *row.Time < 0 || math.IsNaN(*row.Time) || math.IsInf(*row.Time, 0) || row.Type == nil || row.Domain == nil || row.Client == nil || row.Client.IP == nil {
			return Query{}, false
		}
		status, valid := queryString(row.Status, true)
		if !valid {
			return Query{}, false
		}
		return Query{strconv.FormatFloat(*row.Time, 'f', 3, 64), *row.Client.IP, *row.Domain, *row.Type, status, ""}, true
	case Technitium:
		var row *struct {
			At       *string         `json:"timestamp"`
			Client   *string         `json:"clientIpAddress"`
			Name     json.RawMessage `json:"qname"`
			Type     json.RawMessage `json:"qtype"`
			Status   *string         `json:"responseType"`
			Protocol *string         `json:"protocol"`
		}
		if json.Unmarshal(raw, &row) != nil || row == nil || row.At == nil || row.Client == nil || row.Status == nil || row.Protocol == nil {
			return Query{}, false
		}
		// The native writer can report a missing question as two explicit nulls.
		if queryNull(row.Name) != queryNull(row.Type) {
			return Query{}, false
		}
		name, nameValid := queryString(row.Name, true)
		kind, typeValid := queryString(row.Type, true)
		if !nameValid || !typeValid {
			return Query{}, false
		}
		return Query{*row.At, *row.Client, name, kind, *row.Status, *row.Protocol}, true
	default:
		return Query{}, false
	}
}

func queryString(raw json.RawMessage, nullable bool) (string, bool) {
	if nullable && queryNull(raw) {
		return "", true
	}
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return "", false
	}
	return *value, true
}

func queryNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

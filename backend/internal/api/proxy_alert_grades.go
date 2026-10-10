package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A grade needs a full scan, which is heavier than the certificate handshake
// on the ordinary watch schedule. Only an enabled grade rule starts these.
type watchGrade struct {
	watchedDomain
	Grade     string
	Reachable bool
}

type watchGradeScanner func(context.Context, string, int, proxysvc.ScanOptions) *proxysvc.TLSScan

const watchGradeBatch = 12

func gradeRank(grade string) int {
	switch grade {
	case "F":
		return 0
	case "C":
		return 1
	case "B":
		return 2
	case "A":
		return 3
	case "A+":
		return 4
	}
	return -1
}

func watchSubject(d watchedDomain) string {
	subject := fmt.Sprintf("%s:%d", d.Domain, d.Port)
	if d.IP != "" {
		subject += " via " + d.IP
	}
	return subject
}

func (s *Server) checkWatchedGrades(ctx context.Context, scan watchGradeScanner) ([]watchGrade, bool) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, domain, port, ip FROM watched_endpoints WHERE kind = 'tls' ORDER BY domain, port, ip`)
	if err != nil {
		return nil, false
	}
	checks := []watchGrade{}
	for rows.Next() {
		var d watchedDomain
		if err := rows.Scan(&d.ID, &d.Domain, &d.Port, &d.IP); err != nil {
			rows.Close()
			return nil, false
		}
		checks = append(checks, watchGrade{watchedDomain: d})
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, false
	}
	if len(checks) == 0 {
		return checks, true
	}
	// Each batch gets two consecutive passes so the two-check hold can mature.
	// Rotate batches to keep a slow or large watch list within the pass budget.
	pass := s.modules.proxyExtras.alerts.gradePass.Add(1) - 1
	start := int((pass / 2 * watchGradeBatch) % uint64(len(checks)))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for n := 0; n < len(checks) && n < watchGradeBatch; n++ {
		i := (start + n) % len(checks)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return checks, true
		}
		wg.Add(1)
		go func(c *watchGrade) {
			defer wg.Done()
			defer func() { <-sem }()
			protocol, err := proxysvc.ParseStartTLS("", c.Port)
			if err != nil {
				return
			}
			check, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			report := scan(check, c.Domain, c.Port, proxysvc.ScanOptions{ConnectTo: c.IP, StartTLS: protocol})
			if check.Err() == nil && report != nil {
				c.Grade, c.Reachable = report.Grade, report.Reachable
			}
		}(&checks[i])
	}
	wg.Wait()
	return checks, true
}

func watchGradeAlertReading(rule proxyAlertRule, checks []watchGrade) proxyAlertReading {
	reading := newProxyAlertReading()
	minimum := rule.Params.Grade
	if minimum == "" {
		minimum = "A"
	}
	for _, check := range checks {
		subject := watchSubject(check.watchedDomain)
		rank := gradeRank(check.Grade)
		if !check.Reachable || rank < 0 {
			reading.Unknown[subject] = true
			continue
		}
		if rank < gradeRank(minimum) {
			reading.Firing = append(reading.Firing, proxyAlertFinding{
				Subject: subject, Level: proxyAlertFiringLevel, Label: subject,
				Detail: fmt.Sprintf("TLS grade %s is below the expected %s.", check.Grade, minimum),
			})
		} else {
			reading.Now[subject] = fmt.Sprintf("TLS grade %s meets the expected %s.", check.Grade, minimum)
		}
	}
	return reading
}

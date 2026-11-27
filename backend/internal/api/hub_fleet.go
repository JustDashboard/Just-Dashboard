package api

import (
	"context"
	"errors"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
)

// hubFleetSources are where the hub link's fleet fields are read from
// (hub-link-protocol.md 5.4): the Packages page's check, the Certificates
// page's inventory, the deployments' live scans and the audit log. Each is
// read off the heartbeat's path, on its own interval (hublink/fleet.go).
func (s *Server) hubFleetSources() *hublink.FleetSources {
	src := &hublink.FleetSources{SignIns: hublink.AuditSignIns(s.Store.DB)}
	if u := s.modules.updates; u != nil {
		src.OSUpdates = func(ctx context.Context) (*hublink.OSUpdates, error) {
			return hubOSUpdates(u.Check(ctx))
		}
	}
	if p := s.modules.proxy; p != nil {
		src.Certificates = func(ctx context.Context) ([]hublink.CertificateFact, error) {
			certs, err := p.CertificateInventory(ctx)
			if err != nil {
				return nil, err
			}
			facts := make([]hublink.CertificateFact, 0, len(certs))
			for _, c := range certs {
				// One that could not be read has no days to count; the
				// Certificates page says why.
				if c.Error != "" || c.NotAfter.IsZero() {
					continue
				}
				facts = append(facts, hublink.CertificateFact{Name: c.Name, DaysLeft: c.DaysLeft, Issuer: c.Issuer})
			}
			return hublink.SortCertificates(facts), nil
		}
	}
	if d := s.modules.deployRuns; d != nil {
		src.ImageScans = func(ctx context.Context) ([]hublink.ImageScanFact, error) {
			return hubImageScans(d.LiveImageScans(ctx))
		}
	}
	return src
}

// hubOSUpdates is os.updates from the Packages page's check. A host with no
// package manager the dashboard reads has none; a listing that failed is not
// known, rather than "nothing pending".
func hubOSUpdates(rep *updates.Report, err error) (*hublink.OSUpdates, error) {
	if errors.Is(err, updates.ErrNotSupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rep.Error != "" {
		return nil, errors.New(rep.Error)
	}
	out := &hublink.OSUpdates{
		Pending: len(rep.Packages), RebootRequired: rep.RebootRequired, CheckedAt: rep.LastChecked.Unix(),
	}
	if rep.SecurityFiltering {
		n := rep.SecurityCount
		out.Security = &n
	}
	return out, nil
}

// hubImageScans is imageScans from each deployment's live release: its latest
// finished scan, counting only the findings its owner can act on (an upgrade
// of the application's own package, or a rebuild that picks up a fixed base),
// as the deployment's page counts them. Scanner totals would mostly be a
// base image's findings nobody has published a fix for.
func hubImageScans(live []deploy.LiveImageScan, err error) ([]hublink.ImageScanFact, error) {
	if err != nil {
		return nil, err
	}
	facts := make([]hublink.ImageScanFact, 0, len(live))
	for _, l := range live {
		if l.Scan == nil || l.Scan.State != deploy.ImageScanScanned || l.Scan.FinishedAt == nil {
			continue
		}
		facts = append(facts, hublink.ImageScanFact{
			Deployment: l.ProjectName, Environment: l.EnvironmentName,
			Critical: l.Scan.Actionable.Critical, High: l.Scan.Actionable.High,
			ScannedAt: l.Scan.FinishedAt.Unix(),
		})
	}
	return hublink.SortImageScans(facts), nil
}

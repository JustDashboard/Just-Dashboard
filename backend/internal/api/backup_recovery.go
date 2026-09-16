package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

type backupRecoveryChecker struct{ server *Server }

func (s *Server) databaseBackupSources(ctx context.Context, id int64) ([]string, error) {
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, err
	}
	if conn.Driver == dbx.DriverSQLite {
		path, err := s.modules.files.Resolve(info.Database)
		return []string{path}, err
	}
	if !databaseLoopback(info.Host) {
		return nil, errors.New("external databases require a native backup adapter")
	}
	detail, _, err := s.databaseContainer(ctx, conn, info)
	if err != nil {
		return nil, err
	}
	var sources []string
	for _, mount := range detail.Mounts {
		if !mount.RW {
			continue
		}
		switch mount.Type {
		case "volume":
			if mount.Name == "" {
				return nil, errors.New("database volume identity is unavailable")
			}
			sources = append(sources, mount.Name)
		case "bind":
			sources = append(sources, mount.Source)
		default:
			return nil, errors.New("database storage needs a supported backup adapter")
		}
	}
	if len(sources) == 0 {
		return nil, errors.New("database has no observed persistent storage")
	}
	return sources, nil
}

func (o *backupRecoveryChecker) CheckRecovery(ctx context.Context, request backups.RecoveryCheckRequest) (backups.RecoveryCheckResult, error) {
	result := backups.RecoveryCheckResult{}
	if o.server.modules.docker == nil {
		return result, backups.ErrRecoveryUnavailable
	}
	if err := request.Plan.Validate(); err != nil {
		return result, err
	}
	expectedRoot := filepath.Join(o.server.Cfg.DataDir, "staging", "restore-check-"+request.OwnerKey)
	if request.Directory != filepath.Join(expectedRoot, "data") || len(request.OwnerKey) < 12 || strings.ContainsAny(request.OwnerKey, "/\\.\x00") {
		return result, errors.New("restore workspace ownership does not match")
	}
	directory, err := files.New([]string{expectedRoot}).Resolve(request.Directory)
	if err != nil {
		return result, err
	}
	image, err := o.server.modules.docker.InspectImage(ctx, request.Plan.Image)
	if err != nil {
		return result, errors.New("the pinned recovery image is unavailable locally; pull or retain it before verification")
	}
	result.ImageDigest = image.ID
	labels := []dockerx.LabelSpec{
		{Name: "io.just-dashboard.restore-check", Value: strconv.FormatInt(request.VerificationID, 10)},
		{Name: "io.just-dashboard.restore-owner", Value: request.OwnerKey},
	}
	created, err := o.server.modules.docker.Create(ctx, dockerx.ContainerSpec{
		Name:  fmt.Sprintf("jd-restore-%d-%s", request.VerificationID, strings.ToLower(request.OwnerKey[:12])),
		Image: image.ID, Entrypoint: []string{request.Plan.Command[0]}, Command: request.Plan.Command[1:],
		NetworkMode: "none", Mounts: []dockerx.MountSpec{{Type: "bind", Source: directory, Target: "/restore"}},
		Env:    []dockerx.EnvVar{{Name: "JD_RESTORE_ROOT", Value: "/restore"}, {Name: "JD_RESTORE_SCHEMA_VERSION", Value: request.Plan.SchemaVersion}},
		Labels: labels, RestartPolicy: "no", Logging: dockerx.CappedLogging(),
		Limits: dockerx.ResourceLimits{MemoryMB: 512, MemorySwapMB: 512, CPUs: 1, PidsLimit: 128},
		Start:  true,
	}, nil)
	o.record(ctx, "backup.restore.check.start", request.VerificationID, err)
	if err != nil {
		return result, errors.New("the isolated recovery application could not start")
	}
	output, err := o.server.modules.docker.CompletedCommandOutput(ctx, created.ID)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(string(output))))
	result.OutputDigest = "sha256:" + hex.EncodeToString(digest[:])
	return result, nil
}

func (o *backupRecoveryChecker) CleanupRecovery(ctx context.Context, id int64, ownerKey string) error {
	if o.server.modules.docker == nil {
		return backups.ErrRecoveryUnavailable
	}
	labels := map[string]string{"io.just-dashboard.restore-check": strconv.FormatInt(id, 10), "io.just-dashboard.restore-owner": ownerKey}
	containers, err := o.server.modules.docker.ListContainersWithLabels(ctx, labels)
	if err != nil {
		return err
	}
	for _, item := range containers {
		detail, err := o.server.modules.docker.Inspect(ctx, item.ID)
		if err != nil {
			return err
		}
		for name, value := range labels {
			if detail.Labels[name] != value {
				return errors.New("restore container ownership changed")
			}
		}
		err = o.server.modules.docker.RemoveContainer(ctx, item.ID, true, true)
		o.record(ctx, "backup.restore.check.remove", id, err)
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *backupRecoveryChecker) record(ctx context.Context, action string, id int64, err error) {
	detail, _ := json.Marshal(map[string]any{"verificationId": id})
	o.server.Audit.Record(ctx, audit.Entry{Actor: "system", Action: action, Target: strconv.FormatInt(id, 10), Success: err == nil, Detail: string(detail)})
}

package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

type deploymentDatabaseNetworks struct {
	server *Server
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type deploymentDatabaseNetwork struct {
	EnvironmentID  int64
	Name, OwnerKey string
}
type deploymentDatabaseBinding struct {
	ConnectionID                                      int64
	Name, ComposeProject, ComposeService, ContainerID string
}

func databaseURLConnectionIDs(variables map[string]string) ([]int64, error) {
	ids := []int64{}
	for _, value := range variables {
		u, err := url.Parse(value)
		if err != nil || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".jd.internal") {
			continue
		}
		host := strings.ToLower(u.Hostname())
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(host, "db-"), ".jd.internal"), 10, 64)
		if err != nil || id <= 0 || host != databaseDNSName(id) {
			return nil, errors.New("invalid managed database hostname")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (o *deploymentDatabaseNetworks) NetworksForRuntime(ctx context.Context, environmentID int64, plan deploy.RuntimePlanConfig, variables map[string]string) ([]string, error) {
	ids, err := databaseURLConnectionIDs(variables)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	if plan.HostNetwork {
		return nil, errors.New("host-network workloads need the host database URL")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, id := range ids {
		conn, dsn, err := o.server.dbConnRow(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := o.checkEnvironmentIsolation(ctx, environmentID, conn, dsn); err != nil {
			return nil, err
		}
	}
	network, err := o.network(ctx, environmentID, true)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := o.bind(ctx, network, id); err != nil {
			return nil, err
		}
	}
	return []string{network.Name}, nil
}

func (o *deploymentDatabaseNetworks) network(ctx context.Context, environmentID int64, create bool) (deploymentDatabaseNetwork, error) {
	n := deploymentDatabaseNetwork{EnvironmentID: environmentID}
	err := o.server.Store.DB.QueryRowContext(ctx, `SELECT network_name,owner_key FROM deploy_database_networks WHERE environment_id=?`, environmentID).Scan(&n.Name, &n.OwnerKey)
	if errors.Is(err, sql.ErrNoRows) && create {
		var present int
		if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT 1 FROM deploy_environments WHERE id=?`, environmentID).Scan(&present); err != nil {
			return n, deploy.ErrEnvironmentNotFound
		}
		n.OwnerKey = rand.Text()
		n.Name = fmt.Sprintf("jd-e%d-db-%s", environmentID, strings.ToLower(n.OwnerKey[:12]))
		_, err = o.server.Store.DB.ExecContext(ctx, `INSERT INTO deploy_database_networks(environment_id,network_name,owner_key,created_at) VALUES(?,?,?,?)`, environmentID, n.Name, n.OwnerKey, time.Now().UTC().Unix())
	}
	if err != nil {
		return n, err
	}
	if o.server.modules.docker == nil {
		return n, deploy.ErrRuntimeUnavailable
	}
	observed, err := o.server.modules.docker.InspectNetwork(ctx, n.Name)
	if errdefs.IsNotFound(err) && create {
		_, err = o.server.modules.docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: n.Name, Driver: "bridge", Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10), "io.just-dashboard.database-network": n.OwnerKey,
		}})
		o.record(ctx, "deploy.database.network", environmentID, 0, "", err)
		if err == nil {
			observed, err = o.server.modules.docker.InspectNetwork(ctx, n.Name)
		}
	}
	if err != nil {
		return n, err
	}
	if observed.Name != n.Name || observed.Driver != "bridge" || observed.Internal || observed.Labels["io.just-dashboard.database-network"] != n.OwnerKey || observed.Labels["io.just-dashboard.environment-id"] != strconv.FormatInt(environmentID, 10) || observed.Labels["io.just-dashboard.managed"] != "true" {
		return n, errors.New("database network ownership does not match")
	}
	_, err = o.server.Store.DB.ExecContext(ctx, `UPDATE deploy_database_networks SET network_id=? WHERE environment_id=?`, observed.ID, environmentID)
	return n, err
}

func (o *deploymentDatabaseNetworks) bind(ctx context.Context, network deploymentDatabaseNetwork, connectionID int64) error {
	s := o.server
	conn, dsn, err := s.dbConnRow(ctx, connectionID)
	if err != nil {
		return errors.New("linked database connection is unavailable")
	}
	if err := o.checkEnvironmentIsolation(ctx, network.EnvironmentID, conn, dsn); err != nil {
		return err
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil || !databaseLoopback(info.Host) {
		return errors.New("managed database hostname no longer identifies a local container")
	}
	detail, _, err := s.databaseContainer(ctx, conn, info)
	if err != nil {
		return err
	}
	var kind string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, network.EnvironmentID).Scan(&kind); err != nil {
		return err
	}
	// A preview may explicitly link its own database, but cannot join one
	// already serving production under another saved connection identity.
	var crossKind int
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_database_bindings b JOIN deploy_environments e ON e.id=b.environment_id
		WHERE b.environment_id<>? AND (b.connection_id=? OR b.container_name=? OR b.container_id=?) AND (e.kind='preview' OR ?='preview')`, network.EnvironmentID, connectionID, strings.TrimPrefix(detail.Name, "/"), detail.ID, kind).Scan(&crossKind); err != nil {
		return err
	}
	if crossKind > 0 {
		return errors.New("preview and production environments need separate database containers")
	}
	var binding deploymentDatabaseBinding
	err = s.Store.DB.QueryRowContext(ctx, `SELECT connection_id,container_name,compose_project,compose_service,container_id FROM deploy_database_bindings WHERE environment_id=? AND connection_id=?`, network.EnvironmentID, connectionID).
		Scan(&binding.ConnectionID, &binding.Name, &binding.ComposeProject, &binding.ComposeService, &binding.ContainerID)
	if errors.Is(err, sql.ErrNoRows) {
		binding = deploymentDatabaseBinding{ConnectionID: connectionID, Name: strings.TrimPrefix(detail.Name, "/"), ComposeProject: detail.ComposeStack, ComposeService: detail.ComposeSvc}
		_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name,compose_project,compose_service) VALUES(?,?,?,?,?)`, network.EnvironmentID, connectionID, binding.Name, binding.ComposeProject, binding.ComposeService)
	}
	if err != nil {
		return err
	}
	if !databaseBindingMatches(binding, detail) {
		return errors.New("the saved database port belongs to a different container; reconnect the database explicitly")
	}
	if err := o.attachDatabase(ctx, network, binding, detail); err != nil {
		_, _ = s.Store.DB.ExecContext(ctx, `UPDATE deploy_database_bindings SET status='unavailable',checked_at=? WHERE environment_id=? AND connection_id=?`, time.Now().UTC().Unix(), network.EnvironmentID, connectionID)
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, `UPDATE deploy_database_bindings SET container_id=?,container_name=?,status='connected',checked_at=? WHERE environment_id=? AND connection_id=?`, detail.ID, strings.TrimPrefix(detail.Name, "/"), time.Now().UTC().Unix(), network.EnvironmentID, connectionID)
	return err
}

func databaseBindingMatches(binding deploymentDatabaseBinding, detail *dockerx.ContainerDetail) bool {
	if binding.ContainerID != "" && binding.ContainerID == detail.ID {
		return true
	}
	if binding.ComposeProject != "" || binding.ComposeService != "" {
		return binding.ComposeProject != "" && binding.ComposeService != "" && detail.ComposeStack == binding.ComposeProject && detail.ComposeSvc == binding.ComposeService
	}
	return binding.Name == strings.TrimPrefix(detail.Name, "/")
}

func (o *deploymentDatabaseNetworks) attachDatabase(ctx context.Context, network deploymentDatabaseNetwork, binding deploymentDatabaseBinding, detail *dockerx.ContainerDetail) error {
	alias := databaseDNSName(binding.ConnectionID)
	netDetail, err := o.server.modules.docker.NetworkDetail(ctx, network.Name)
	if err != nil {
		return err
	}
	for _, member := range netDetail.Members {
		if member.ID != detail.ID && member.ID != binding.ContainerID && slices.Contains(member.Aliases, alias) {
			return errors.New("the database alias is already used by another container")
		}
	}
	if binding.ContainerID != "" && binding.ContainerID != detail.ID {
		old, err := o.server.modules.docker.Inspect(ctx, binding.ContainerID)
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		if err == nil {
			for _, endpoint := range old.NetworkList {
				if endpoint.Name == network.Name && slices.Contains(endpoint.Aliases, alias) {
					if old.State == "running" {
						return errors.New("the previous database container is still running on the managed alias")
					}
					err := o.server.modules.docker.DisconnectNetwork(ctx, network.Name, old.ID, false)
					o.record(ctx, "deploy.database.detach", network.EnvironmentID, binding.ConnectionID, old.ID, err)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	aliases := []string{alias}
	for _, endpoint := range detail.NetworkList {
		if endpoint.Name == network.Name {
			if slices.Contains(endpoint.Aliases, alias) {
				return nil
			}
			aliases = append(aliases, endpoint.Aliases...)
			// Docker recreate preserves the network but may omit its aliases.
			// Only this environment-owned endpoint is replaced.
			err := o.server.modules.docker.DisconnectNetwork(ctx, network.Name, detail.ID, false)
			o.record(ctx, "deploy.database.detach", network.EnvironmentID, binding.ConnectionID, detail.ID, err)
			if err != nil {
				return err
			}
		}
	}
	err = o.server.modules.docker.ConnectNetwork(ctx, network.Name, detail.ID, aliases)
	o.record(ctx, "deploy.database.attach", network.EnvironmentID, binding.ConnectionID, detail.ID, err)
	return err
}

func (o *deploymentDatabaseNetworks) ResolveVariable(ctx context.Context, environmentID int64, revision int, target string) (string, error) {
	var id int64
	for _, candidate := range []string{target, strings.TrimSuffix(target, ".url")} {
		if parsed, err := strconv.ParseInt(candidate, 10, 64); err == nil && parsed > 0 {
			id = parsed
			break
		}
		if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT id FROM db_connections WHERE name=?`, candidate).Scan(&id); err == nil {
			break
		}
	}
	if id <= 0 {
		return "", errors.New("database reference target was not found")
	}
	conn, dsn, err := o.server.dbConnRow(ctx, id)
	if err != nil {
		return "", errors.New("database reference could not be opened")
	}
	if err := o.checkEnvironmentIsolation(ctx, environmentID, conn, dsn); err != nil {
		return "", err
	}
	var raw string
	if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT config_json FROM deploy_runtime_plans WHERE environment_id=? AND revision=?`, environmentID, revision).Scan(&raw); err != nil {
		return "", err
	}
	var plan deploy.RuntimePlanConfig
	if json.Unmarshal([]byte(raw), &plan) != nil {
		return "", deploy.ErrInvalidPlan
	}
	if plan.HostNetwork {
		return dsn, nil
	}
	return o.server.databaseApplicationURL(ctx, conn, dsn)
}

func (o *deploymentDatabaseNetworks) record(ctx context.Context, action string, environmentID, connectionID int64, containerID string, err error) {
	raw, _ := json.Marshal(map[string]any{"environmentId": environmentID, "connectionId": connectionID, "containerId": containerID})
	o.server.Audit.Record(ctx, audit.Entry{Actor: "system", Action: action, Target: strconv.FormatInt(environmentID, 10), Success: err == nil, Detail: string(raw)})
}

func (o *deploymentDatabaseNetworks) Reconcile(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	rows, err := o.server.Store.DB.QueryContext(ctx, `SELECT environment_id,connection_id FROM deploy_database_bindings ORDER BY environment_id,connection_id`)
	if err != nil {
		return err
	}
	type item struct{ environmentID, connectionID int64 }
	items := []item{}
	for rows.Next() {
		var v item
		if err := rows.Scan(&v.environmentID, &v.connectionID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	var errs []error
	for _, v := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		n, err := o.network(opCtx, v.environmentID, true)
		if err == nil {
			err = o.bind(opCtx, n, v.connectionID)
		}
		if err == nil {
			err = o.attachApplications(opCtx, n)
		}
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("environment %d database %d needs reconnection", v.environmentID, v.connectionID))
			_, _ = o.server.Store.DB.ExecContext(ctx, `UPDATE deploy_database_bindings SET status='unavailable',checked_at=? WHERE environment_id=? AND connection_id=?`, time.Now().UTC().Unix(), v.environmentID, v.connectionID)
		}
	}
	return errors.Join(errs...)
}

func (o *deploymentDatabaseNetworks) attachApplications(ctx context.Context, network deploymentDatabaseNetwork) error {
	containers, err := o.server.modules.docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(network.EnvironmentID, 10)})
	if err != nil {
		return err
	}
	for _, container := range containers {
		detail, err := o.server.modules.docker.Inspect(ctx, container.ID)
		if err != nil {
			return err
		}
		found := false
		for _, endpoint := range detail.NetworkList {
			if endpoint.Name == network.Name {
				found = true
			}
		}
		if found {
			continue
		}
		if detail.NetworkMode == "host" || detail.NetworkMode == "none" || strings.HasPrefix(detail.NetworkMode, "container:") {
			continue
		}
		err = o.server.modules.docker.ConnectNetwork(ctx, network.Name, detail.ID, nil)
		o.record(ctx, "deploy.database.application.attach", network.EnvironmentID, 0, detail.ID, err)
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *deploymentDatabaseNetworks) Start(ctx context.Context) {
	if o == nil || o.cancel != nil {
		return
	}
	ctx, o.cancel = context.WithCancel(ctx)
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			_ = o.Reconcile(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (o *deploymentDatabaseNetworks) Stop() {
	if o == nil || o.cancel == nil {
		return
	}
	o.cancel()
	<-o.done
	o.cancel = nil
}

func (o *deploymentDatabaseNetworks) RemoveRuntimeNetworks(ctx context.Context, environmentID int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.removeRuntimeNetworks(ctx, environmentID, "")
}

func (o *deploymentDatabaseNetworks) removeRuntimeNetworks(ctx context.Context, environmentID int64, expectedNetworkID string) error {
	n, err := o.network(ctx, environmentID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err == nil {
		observed, err := o.server.modules.docker.InspectNetwork(ctx, n.Name)
		if err != nil {
			return err
		}
		if expectedNetworkID != "" && observed.ID != expectedNetworkID {
			return deploy.ErrRemovalPlanChanged
		}
		var ids []string
		for id := range observed.Containers {
			var allowed int
			if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_database_bindings WHERE environment_id=? AND container_id=?`, environmentID, id).Scan(&allowed); err != nil {
				return err
			}
			if allowed == 0 {
				return errors.New("remove application containers before removing their database network")
			}
			ids = append(ids, id)
		}
		for _, id := range ids {
			err := o.server.modules.docker.DisconnectNetwork(ctx, n.Name, id, false)
			o.record(ctx, "deploy.database.detach", environmentID, 0, id, err)
			if err != nil {
				return err
			}
		}
		err = o.server.modules.docker.RemoveNetwork(ctx, n.Name)
		o.record(ctx, "deploy.database.network.remove", environmentID, 0, "", err)
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	tx, err := o.server.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_database_bindings WHERE environment_id=?`, environmentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_database_networks SET network_id='' WHERE environment_id=?`, environmentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (o *deploymentDatabaseNetworks) RemoveNetworkByID(ctx context.Context, networkID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var environmentID int64
	if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT environment_id FROM deploy_database_networks WHERE network_id=?`, networkID).Scan(&environmentID); err != nil {
		return errors.New("managed database network was not found")
	}
	return o.removeRuntimeNetworks(ctx, environmentID, networkID)
}

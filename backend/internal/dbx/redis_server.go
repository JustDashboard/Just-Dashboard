package dbx

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The server itself, as opposed to what is stored in it: what it is, how hard
// it is working, who is connected, how it is configured, and whether what it
// holds would survive a restart. Nearly all of it comes from INFO, which is
// one text block of several hundred lines; the functions here turn the parts
// an operator acts on into fields, and hand the rest over by section.

// RedisKeyspace is one logical database's line of INFO keyspace.
type RedisKeyspace struct {
	DB      int   `json:"db"`
	Keys    int64 `json:"keys"`
	Expires int64 `json:"expires"`
	// AvgTTLMs is the mean time to live of the keys that have one, in
	// milliseconds; zero when the server does not report it.
	AvgTTLMs int64 `json:"avgTtlMs"`
}

// parseRedisKeyspace reads "db0:keys=2610,expires=1800,avg_ttl=7000926".
func parseRedisKeyspace(sections []RedisInfoSection) []RedisKeyspace {
	out := []RedisKeyspace{}
	for _, s := range sections {
		for _, f := range s.Fields {
			if !strings.HasPrefix(f.Name, "db") {
				continue
			}
			idx, err := strconv.Atoi(f.Name[2:])
			if err != nil {
				continue
			}
			ks := RedisKeyspace{DB: idx}
			for name, value := range redisInfoPairs(f.Value) {
				n, _ := strconv.ParseInt(value, 10, 64)
				switch name {
				case "keys":
					ks.Keys = n
				case "expires":
					ks.Expires = n
				case "avg_ttl":
					if n > 0 {
						ks.AvgTTLMs = n
					}
				}
			}
			out = append(out, ks)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DB < out[j].DB })
	return out
}

// redisInfoPairs reads the "a=1,b=2" value some INFO lines carry.
func redisInfoPairs(value string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		if name, v, ok := strings.Cut(part, "="); ok {
			out[strings.TrimSpace(name)] = strings.TrimSpace(v)
		}
	}
	return out
}

func redisInfoInt(info map[string]string, name string) int64 {
	n, err := strconv.ParseInt(info[name], 10, 64)
	if err != nil {
		f, _ := strconv.ParseFloat(info[name], 64)
		return int64(f)
	}
	return n
}

// RedisMemoryFacts is the memory section, reduced to what a gauge is drawn
// from.
type RedisMemoryFacts struct {
	Used int64 `json:"used"`
	RSS  int64 `json:"rss"`
	Peak int64 `json:"peak"`
	// Max is maxmemory; zero means no limit is set.
	Max    int64  `json:"max"`
	Policy string `json:"policy,omitempty"`
	// FragmentationRatio is RSS over used; well above 1 is memory the
	// allocator holds and the data does not.
	FragmentationRatio float64 `json:"fragmentationRatio,omitempty"`
	SystemTotal        int64   `json:"systemTotal,omitempty"`
}

// RedisPersistence is whether what the server holds would survive a restart,
// and when it was last written down.
type RedisPersistence struct {
	// Loading is true while the server is still reading its data back in.
	Loading bool `json:"loading"`
	// Dir and File are where the snapshot is written, when the server will
	// say.
	Dir  string        `json:"dir,omitempty"`
	File string        `json:"file,omitempty"`
	RDB  RedisRDBFacts `json:"rdb"`
	AOF  RedisAOFFacts `json:"aof"`
}

// RedisRDBFacts is the snapshot half of persistence.
type RedisRDBFacts struct {
	// Schedule is the save setting — "3600 1 300 100" is "after an hour if one
	// key changed, after five minutes if a hundred did". Empty is snapshots
	// switched off; nil is a server that would not say.
	Schedule         *string    `json:"schedule,omitempty"`
	LastSaveAt       *time.Time `json:"lastSaveAt,omitempty"`
	ChangesSinceSave int64      `json:"changesSinceSave"`
	InProgress       bool       `json:"inProgress"`
	// LastStatus is "ok" or "err".
	LastStatus string `json:"lastStatus,omitempty"`
	// LastDurationSeconds is how long the last background save took.
	LastDurationSeconds *int64 `json:"lastDurationSeconds,omitempty"`
}

// RedisAOFFacts is the append-only-file half.
type RedisAOFFacts struct {
	// Supported is false on a flavour with no append-only file at all.
	Supported         bool   `json:"supported"`
	Enabled           bool   `json:"enabled"`
	RewriteInProgress bool   `json:"rewriteInProgress"`
	LastRewriteStatus string `json:"lastRewriteStatus,omitempty"`
	LastWriteStatus   string `json:"lastWriteStatus,omitempty"`
	CurrentSize       int64  `json:"currentSize,omitempty"`
	BaseSize          int64  `json:"baseSize,omitempty"`
	// Fsync is appendfsync: always, everysec or no.
	Fsync string `json:"fsync,omitempty"`
}

// RedisReplica is one replica as its primary sees it.
type RedisReplica struct {
	Addr       string `json:"addr"`
	State      string `json:"state"`
	Offset     int64  `json:"offset"`
	LagSeconds int64  `json:"lagSeconds"`
}

// RedisPrimaryLink is a replica's connection to its primary.
type RedisPrimaryLink struct {
	Addr string `json:"addr"`
	// Up is whether the link is established right now.
	Up               bool  `json:"up"`
	LastIOSecondsAgo int64 `json:"lastIoSecondsAgo"`
	Syncing          bool  `json:"syncing"`
	Offset           int64 `json:"offset"`
	ReadOnly         bool  `json:"readOnly"`
}

// RedisReplication is the server's place in a replication topology.
type RedisReplication struct {
	// Role is "primary" or "replica", whatever word the server used for it.
	Role     string         `json:"role"`
	Offset   int64          `json:"offset"`
	Replicas []RedisReplica `json:"replicas"`
	// Primary is set on a replica.
	Primary *RedisPrimaryLink `json:"primary,omitempty"`
}

// RedisClusterFacts is what a cluster node says about the cluster.
type RedisClusterFacts struct {
	State         string `json:"state"`
	SlotsAssigned int64  `json:"slotsAssigned"`
	KnownNodes    int64  `json:"knownNodes"`
	Size          int64  `json:"size"`
}

// RedisServer is the server at a glance.
type RedisServer struct {
	*RedisProfile
	Role string `json:"role"`
	// DB is the logical database the connection string names: the one a key
	// request reads when it does not ask for another.
	DB int `json:"db"`
	// Databases is how many logical databases the server has.
	Databases     int   `json:"databases"`
	UptimeSeconds int64 `json:"uptimeSeconds"`
	SampledAtMs   int64 `json:"sampledAtMs"`
	// Notice is set when the endpoint is not a plain single server and the
	// operator should know before anything else: a cluster node, a sentinel.
	Notice      string             `json:"notice,omitempty"`
	ConfigFile  string             `json:"configFile,omitempty"`
	Keyspace    []RedisKeyspace    `json:"keyspace"`
	Memory      RedisMemoryFacts   `json:"memory"`
	Persistence RedisPersistence   `json:"persistence"`
	Replication RedisReplication   `json:"replication"`
	Cluster     *RedisClusterFacts `json:"cluster,omitempty"`
	// Sections is all of INFO, as printed, for the table of everything.
	Sections []RedisInfoSection `json:"sections"`
}

const (
	redisClusterNotice  = "This is one node of a Redis Cluster. The dashboard talks to this node alone: it lists the keys this node holds, and a key that lives on another node answers with where it lives."
	redisSentinelNotice = "This is a Redis Sentinel. It watches other Redis servers and holds no keys of its own; connect to the master it reports to browse data."
)

// RedisServerOverview reads the server's identity, state and INFO.
func RedisServerOverview(ctx context.Context, client *redis.Client) (*RedisServer, error) {
	profile, err := RedisProbe(ctx, client)
	if err != nil {
		return nil, err
	}
	pipe := client.Pipeline()
	infoCmd := pipe.Info(ctx)
	// Asked for one at a time: CONFIG GET with several names is Redis 7.
	settings := map[string]*redis.Cmd{}
	if profile.Features.Config {
		for _, name := range []string{"databases", "save", "dir", "dbfilename", "appendfsync"} {
			settings[name] = redisConfigGet(ctx, pipe, name)
		}
	}
	var clusterCmd *redis.StringCmd
	if profile.Mode == "cluster" {
		clusterCmd = pipe.ClusterInfo(ctx)
	}
	_, _ = pipe.Exec(ctx)
	sampled := time.Now()
	raw, err := infoCmd.Result()
	if err != nil {
		return nil, err
	}
	sections := parseRedisInfo(raw)
	info := redisInfoMap(sections)
	// A server that would not answer a setting leaves it out of the map, and
	// the facts built from it stay unknown rather than reading as empty.
	setting := func(name string) (string, bool) {
		cmd, asked := settings[name]
		if !asked {
			return "", false
		}
		return redisConfigValue(cmd, name)
	}

	out := &RedisServer{
		RedisProfile: profile, DB: client.Options().DB, Databases: 16,
		UptimeSeconds: redisInfoInt(info, "uptime_in_seconds"),
		SampledAtMs:   sampled.UnixMilli(), ConfigFile: info["config_file"],
		Keyspace: parseRedisKeyspace(sections), Sections: sections,
	}
	if v, ok := setting("databases"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.Databases = n
		}
	}
	switch profile.Mode {
	case "cluster":
		out.Databases, out.Notice = 1, redisClusterNotice
		if clusterCmd != nil && clusterCmd.Err() == nil {
			ci := redisInfoMap(parseRedisInfo(clusterCmd.Val()))
			out.Cluster = &RedisClusterFacts{
				State:         ci["cluster_state"],
				SlotsAssigned: redisInfoInt(ci, "cluster_slots_assigned"),
				KnownNodes:    redisInfoInt(ci, "cluster_known_nodes"),
				Size:          redisInfoInt(ci, "cluster_size"),
			}
		}
	case "sentinel":
		out.Databases, out.Notice = 0, redisSentinelNotice
	}

	out.Memory = RedisMemoryFacts{
		Used: redisInfoInt(info, "used_memory"), RSS: redisInfoInt(info, "used_memory_rss"),
		Peak: redisInfoInt(info, "used_memory_peak"), Max: redisInfoInt(info, "maxmemory"),
		Policy: info["maxmemory_policy"], SystemTotal: redisInfoInt(info, "total_system_memory"),
	}
	out.Memory.FragmentationRatio, _ = strconv.ParseFloat(info["mem_fragmentation_ratio"], 64)

	out.Persistence = redisPersistenceFacts(info, profile)
	out.Persistence.Dir, _ = setting("dir")
	out.Persistence.File, _ = setting("dbfilename")
	out.Persistence.AOF.Fsync, _ = setting("appendfsync")
	if v, ok := setting("save"); ok {
		out.Persistence.RDB.Schedule = &v
	}

	out.Replication = redisReplicationFacts(info)
	out.Role = out.Replication.Role
	if profile.Mode == "sentinel" {
		out.Role = "sentinel"
	}
	return out, nil
}

// redisPersistenceFacts reads INFO persistence. Dragonfly names the same
// facts differently — last_success_save for rdb_last_save_time, and so on —
// and both spellings are read.
func redisPersistenceFacts(info map[string]string, profile *RedisProfile) RedisPersistence {
	first := func(names ...string) string { return redisFirst(info, names...) }
	p := RedisPersistence{Loading: info["loading"] == "1"}
	if ts, err := strconv.ParseInt(first("rdb_last_save_time", "last_success_save"), 10, 64); err == nil && ts > 0 {
		at := time.Unix(ts, 0).UTC()
		p.RDB.LastSaveAt = &at
	}
	p.RDB.ChangesSinceSave, _ = strconv.ParseInt(
		first("rdb_changes_since_last_save", "rdb_changes_since_last_success_save"), 10, 64)
	p.RDB.InProgress = first("rdb_bgsave_in_progress", "saving") == "1"
	p.RDB.LastStatus = info["rdb_last_bgsave_status"]
	if d, err := strconv.ParseInt(first("rdb_last_bgsave_time_sec", "last_success_save_duration_sec"), 10, 64); err == nil && d >= 0 {
		p.RDB.LastDurationSeconds = &d
	}
	p.AOF = RedisAOFFacts{
		Supported:         profile.Features.AOF,
		Enabled:           info["aof_enabled"] == "1",
		RewriteInProgress: info["aof_rewrite_in_progress"] == "1",
		LastRewriteStatus: info["aof_last_bgrewrite_status"],
		LastWriteStatus:   info["aof_last_write_status"],
		CurrentSize:       redisInfoInt(info, "aof_current_size"),
		BaseSize:          redisInfoInt(info, "aof_base_size"),
	}
	return p
}

func redisReplicationFacts(info map[string]string) RedisReplication {
	r := RedisReplication{Role: "primary", Replicas: []RedisReplica{}, Offset: redisInfoInt(info, "master_repl_offset")}
	if redisRole(info["role"]) == "replica" {
		r.Role = "replica"
		r.Primary = &RedisPrimaryLink{
			Addr:             info["master_host"] + ":" + info["master_port"],
			Up:               info["master_link_status"] == "up",
			LastIOSecondsAgo: redisInfoInt(info, "master_last_io_seconds_ago"),
			Syncing:          info["master_sync_in_progress"] == "1",
			Offset:           redisInfoInt(info, "slave_repl_offset"),
			ReadOnly:         redisFirst(info, "slave_read_only", "replica_read_only") == "1",
		}
	}
	// "slave0:ip=10.0.0.5,port=6379,state=online,offset=1234,lag=0"
	for i := 0; ; i++ {
		line, ok := info["slave"+strconv.Itoa(i)]
		if !ok {
			if line, ok = info["replica"+strconv.Itoa(i)]; !ok {
				break
			}
		}
		fields := redisInfoPairs(line)
		replica := RedisReplica{Addr: fields["ip"] + ":" + fields["port"], State: fields["state"]}
		replica.Offset, _ = strconv.ParseInt(fields["offset"], 10, 64)
		replica.LagSeconds, _ = strconv.ParseInt(fields["lag"], 10, 64)
		r.Replicas = append(r.Replicas, replica)
	}
	return r
}

// redisFirst is the first of several spellings of a field that the server
// actually printed.
func redisFirst(info map[string]string, names ...string) string {
	for _, n := range names {
		if v, ok := info[n]; ok {
			return v
		}
	}
	return ""
}

// RedisCommandStat is one command's line of INFO commandstats.
type RedisCommandStat struct {
	Command string `json:"command"`
	Calls   int64  `json:"calls"`
	// Usec is the total time the server spent in the command, in
	// microseconds, since it started or its statistics were reset.
	Usec        int64   `json:"usec"`
	UsecPerCall float64 `json:"usecPerCall"`
	// Rejected and Failed are calls refused before running and calls that
	// ran and returned an error, on a server that counts them.
	Rejected *int64 `json:"rejected,omitempty"`
	Failed   *int64 `json:"failed,omitempty"`
	// P50, P99 and P999 are how long a call took, in microseconds, at those
	// percentiles — which a mean hides. Redis 7 and Valkey track them; absent
	// elsewhere.
	P50  *float64 `json:"p50Us,omitempty"`
	P99  *float64 `json:"p99Us,omitempty"`
	P999 *float64 `json:"p999Us,omitempty"`
}

// RedisCommandStats is what the server has spent its time on.
type RedisCommandStats struct {
	// Commands are sorted by total time, the most expensive first.
	Commands    []RedisCommandStat `json:"commands"`
	TotalCalls  int64              `json:"totalCalls"`
	TotalUsec   int64              `json:"totalUsec"`
	SampledAtMs int64              `json:"sampledAtMs"`
}

// RedisCommandStatistics reads INFO commandstats, and the latency percentiles
// beside it where the server keeps them.
func RedisCommandStatistics(ctx context.Context, client *redis.Client) (*RedisCommandStats, error) {
	pipe := client.Pipeline()
	stats := pipe.Info(ctx, "commandstats")
	latency := pipe.Info(ctx, "latencystats")
	_, _ = pipe.Exec(ctx)
	raw, err := stats.Result()
	if err != nil {
		return nil, err
	}
	// "latency_percentiles_usec_get:p50=1.003,p99=2.007,p99.9=5.023". A
	// server with no such section answers with nothing, or with an error for
	// the one command, and the percentiles are simply not there.
	percentiles := map[string]map[string]string{}
	for _, s := range parseRedisInfo(latency.Val()) {
		for _, f := range s.Fields {
			if name, ok := strings.CutPrefix(f.Name, "latency_percentiles_usec_"); ok {
				percentiles[name] = redisInfoPairs(f.Value)
			}
		}
	}
	percentile := func(command, which string) *float64 {
		v, ok := percentiles[command][which]
		if !ok {
			return nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		return &f
	}
	out := &RedisCommandStats{Commands: []RedisCommandStat{}, SampledAtMs: time.Now().UnixMilli()}
	for _, s := range parseRedisInfo(raw) {
		for _, f := range s.Fields {
			name, ok := strings.CutPrefix(f.Name, "cmdstat_")
			if !ok {
				continue
			}
			fields := redisInfoPairs(f.Value)
			stat := RedisCommandStat{Command: strings.ToUpper(strings.ReplaceAll(name, "|", " "))}
			stat.Calls, _ = strconv.ParseInt(fields["calls"], 10, 64)
			stat.Usec, _ = strconv.ParseInt(fields["usec"], 10, 64)
			stat.UsecPerCall, _ = strconv.ParseFloat(fields["usec_per_call"], 64)
			if v, ok := fields["rejected_calls"]; ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				stat.Rejected = &n
			}
			if v, ok := fields["failed_calls"]; ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				stat.Failed = &n
			}
			stat.P50, stat.P99, stat.P999 = percentile(name, "p50"), percentile(name, "p99"), percentile(name, "p99.9")
			out.TotalCalls += stat.Calls
			out.TotalUsec += stat.Usec
			out.Commands = append(out.Commands, stat)
		}
	}
	sort.SliceStable(out.Commands, func(i, j int) bool {
		a, b := out.Commands[i], out.Commands[j]
		if a.Usec != b.Usec {
			return a.Usec > b.Usec
		}
		return a.Command < b.Command
	})
	return out, nil
}

// RedisLatencyEvent is one event the latency monitor recorded.
type RedisLatencyEvent struct {
	Event    string    `json:"event"`
	At       time.Time `json:"at"`
	LatestMs int64     `json:"latestMs"`
	MaxMs    int64     `json:"maxMs"`
}

// RedisLatency is the latency monitor's most recent spikes.
type RedisLatency struct {
	// Supported is false on a server with no latency monitor.
	Supported bool `json:"supported"`
	// Enabled is false when the monitor's threshold is zero, which is its
	// default: nothing is recorded until somebody sets one.
	Enabled     bool                `json:"enabled"`
	ThresholdMs *int64              `json:"thresholdMs,omitempty"`
	Events      []RedisLatencyEvent `json:"events"`
	Reason      string              `json:"reason,omitempty"`
}

// RedisLatencyLatest reads LATENCY LATEST where the server has it.
func RedisLatencyLatest(ctx context.Context, client *redis.Client, profile *RedisProfile) (*RedisLatency, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	out := &RedisLatency{Events: []RedisLatencyEvent{}}
	if !profile.Features.Latency {
		out.Reason = redisProductName(profile) + " has no latency monitor."
		return out, nil
	}
	out.Supported = true
	pipe := client.Pipeline()
	latest := pipe.Do(ctx, "LATENCY", "LATEST")
	var threshold *redis.Cmd
	if profile.Features.Config {
		threshold = redisConfigGet(ctx, pipe, "latency-monitor-threshold")
	}
	_, _ = pipe.Exec(ctx)
	if threshold != nil {
		if v, ok := redisConfigValue(threshold, "latency-monitor-threshold"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out.ThresholdMs = &n
				out.Enabled = n > 0
			}
		}
	}
	raw, err := latest.Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	for _, item := range redisSlice(raw) {
		fields := redisSlice(item)
		if len(fields) < 4 {
			continue
		}
		ev := RedisLatencyEvent{Event: redisText(fields[0])}
		ts, _ := redisInt(fields[1])
		ev.At = time.Unix(ts, 0).UTC()
		ev.LatestMs, _ = redisInt(fields[2])
		ev.MaxMs, _ = redisInt(fields[3])
		out.Events = append(out.Events, ev)
	}
	if len(out.Events) > 0 {
		out.Enabled = true
	}
	if !out.Enabled && out.ThresholdMs != nil {
		out.Reason = "The latency monitor is off (latency-monitor-threshold is 0), so nothing has been recorded."
	}
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].MaxMs > out.Events[j].MaxMs })
	return out, nil
}

// RedisClientInfo is one connected client, from CLIENT LIST.
type RedisClientInfo struct {
	ID        int64  `json:"id"`
	Addr      string `json:"addr"`
	LocalAddr string `json:"laddr,omitempty"`
	Name      string `json:"name"`
	User      string `json:"user,omitempty"`
	// AgeSeconds is how long it has been connected; IdleSeconds how long
	// since it last sent anything.
	AgeSeconds  int64 `json:"ageSeconds"`
	IdleSeconds int64 `json:"idleSeconds"`
	DB          int   `json:"db"`
	// Command is the last command it ran, by name only.
	Command string `json:"command"`
	// Flags are the server's own letters: N for a normal client, S a replica,
	// M a primary, P a subscriber, x in a transaction, b blocked.
	Flags         string `json:"flags"`
	Subscriptions int64  `json:"subscriptions"`
	InTransaction bool   `json:"inTransaction"`
	// OutputMemory is what the server is holding to send it; a number that
	// keeps growing is a client not reading its replies.
	OutputMemory int64  `json:"outputMemory"`
	TotalMemory  int64  `json:"totalMemory"`
	Library      string `json:"library,omitempty"`
	// Self marks the connection this list was read over.
	Self bool `json:"self"`
}

// RedisClients lists the connections the server has open.
func RedisClients(ctx context.Context, client *redis.Client) ([]RedisClientInfo, error) {
	pipe := client.Pipeline()
	list := pipe.ClientList(ctx)
	self := pipe.ClientID(ctx)
	_, _ = pipe.Exec(ctx)
	raw, err := list.Result()
	if err != nil {
		return nil, err
	}
	// Zero is not an id any server gives out, so an unanswered CLIENT ID
	// marks nothing.
	own := self.Val()
	out := []RedisClientInfo{}
	for _, line := range strings.Split(raw, "\n") {
		c, ok := parseRedisClient(line)
		if !ok {
			continue
		}
		c.Self = own != 0 && c.ID == own
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// parseRedisClient reads one "id=4 addr=10.0.0.1:37350 name= …" line. A
// client name cannot contain a space, so splitting on them is exact.
func parseRedisClient(line string) (RedisClientInfo, bool) {
	fields := map[string]string{}
	for _, part := range strings.Fields(line) {
		if name, value, ok := strings.Cut(part, "="); ok {
			fields[name] = value
		}
	}
	id, err := strconv.ParseInt(fields["id"], 10, 64)
	if err != nil {
		return RedisClientInfo{}, false
	}
	num := func(name string) int64 {
		n, _ := strconv.ParseInt(fields[name], 10, 64)
		return n
	}
	c := RedisClientInfo{
		ID: id, Addr: fields["addr"], LocalAddr: fields["laddr"], Name: fields["name"],
		User: fields["user"], AgeSeconds: num("age"), IdleSeconds: num("idle"),
		DB: int(num("db")), Flags: fields["flags"],
		Command:       strings.ToUpper(strings.ReplaceAll(fields["cmd"], "|", " ")),
		Subscriptions: num("sub") + num("psub") + num("ssub"),
		OutputMemory:  num("omem"), TotalMemory: num("tot-mem"),
	}
	// multi is the number of commands queued in a transaction, and -1 — or 0
	// on a server that counts from there — outside one.
	if _, ok := fields["multi"]; ok {
		c.InTransaction = num("multi") > 0 || strings.Contains(c.Flags, "x")
	}
	if lib := fields["lib-name"]; lib != "" {
		c.Library = lib
		if ver := fields["lib-ver"]; ver != "" {
			c.Library += " " + ver
		}
	}
	return c, true
}

// RedisKillClient disconnects one client by id. It reports whether there was
// one to disconnect.
func RedisKillClient(ctx context.Context, client *redis.Client, id int64) (bool, error) {
	if id <= 0 {
		return false, fmt.Errorf("a client id is required")
	}
	n, err := client.Do(ctx, "CLIENT", "KILL", "ID", strconv.FormatInt(id, 10)).Int64()
	if err != nil {
		// Redis answers a missing id with an error rather than a zero.
		if strings.Contains(strings.ToLower(err.Error()), "no such client") {
			return false, nil
		}
		return false, err
	}
	return n > 0, nil
}

// RedisConfigParam is one configuration parameter.
type RedisConfigParam struct {
	Name string `json:"name"`
	// Value is the parameter as the server holds it — and always empty for a
	// secret, whose value never leaves the server.
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
	// Set says whether a secret has a value at all, which is the one thing
	// about a password that is safe to show.
	Set *bool `json:"set,omitempty"`
}

// RedisConfigGroup is the parameters about one subject.
type RedisConfigGroup struct {
	Name   string             `json:"name"`
	Params []RedisConfigParam `json:"params"`
}

// RedisConfig is the running configuration.
type RedisConfig struct {
	// Supported is false where CONFIG is unavailable — a managed service that
	// removed it — and Reason says so.
	Supported bool               `json:"supported"`
	Reason    string             `json:"reason,omitempty"`
	Groups    []RedisConfigGroup `json:"groups"`
	// ConfigFile is the file the server was started from. Without one a
	// change made here lasts until the next restart and cannot be written
	// down.
	ConfigFile string `json:"configFile,omitempty"`
	Rewritable bool   `json:"rewritable"`
}

// redisConfigGet queues a CONFIG GET and leaves its reply unparsed.
//
// The driver's own reader expects every value to be a string. KeyDB answers
// some parameters with a list, and one such value fails the whole reply — so
// the reply is read here, where a list can be joined instead.
func redisConfigGet(ctx context.Context, pipe redis.Pipeliner, pattern string) *redis.Cmd {
	return pipe.Do(ctx, "CONFIG", "GET", pattern)
}

// redisConfigValues reads a CONFIG GET reply as parameter names and values.
func redisConfigValues(cmd *redis.Cmd) map[string]string {
	out := map[string]string{}
	raw, err := cmd.Result()
	if err != nil {
		return out
	}
	for name, value := range redisPairs(raw) {
		if list, ok := value.([]any); ok {
			out[name] = strings.Join(redisStringList(list), " ")
			continue
		}
		out[name] = redisText(value)
	}
	return out
}

// redisConfigValue reads one parameter out of a CONFIG GET reply, whichever
// way the server spells it: Dragonfly writes slowlog_max_len where every
// other flavour writes slowlog-max-len.
func redisConfigValue(cmd *redis.Cmd, name string) (string, bool) {
	for key, value := range redisConfigValues(cmd) {
		if redisConfigName(key) == name {
			return value, true
		}
	}
	return "", false
}

// redisRole is a replication role in the words the dashboard uses for every
// engine, whichever pair the server printed.
func redisRole(role string) string {
	switch role {
	case "slave", "replica":
		return "replica"
	case "master", "primary":
		return "primary"
	}
	return role
}

// redisConfigOrder is the order the groups are shown in.
var redisConfigOrder = []string{
	"general", "network", "security", "memory", "persistence", "replication",
	"limits", "logging", "data types", "scripting", "cluster", "other",
}

// redisConfigName folds Dragonfly's underscores into the dashes every other
// flavour uses, so one set of rules reads all of them.
func redisConfigName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

// RedisConfigSecret reports whether a parameter holds a credential. Its value
// is never read out and never written to the audit trail.
//
// It errs towards hiding: a parameter with "pass" in its name that turns out
// to be harmless costs an operator one lookup in the console, where the one
// that was a password and was not recognised would have been on screen.
func RedisConfigSecret(name string) bool {
	n := redisConfigName(name)
	return n == "masterauth" || n == "primaryauth" || strings.Contains(n, "pass") ||
		strings.Contains(n, "secret") || strings.Contains(n, "token")
}

func redisConfigGroup(name string) string {
	n := redisConfigName(name)
	has := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(n, p) {
				return true
			}
		}
		return false
	}
	switch {
	case RedisConfigSecret(n), has("acl", "protected-mode", "tls", "masteruser", "primaryuser",
		"enable-debug-command", "enable-protected-configs", "enable-module-command", "sanitize-dump"):
		return "security"
	case has("maxmemory", "lazyfree", "active-defrag", "activedefrag", "lfu-", "active-expire",
		"jemalloc", "replica-lazy-flush", "slave-lazy-flush", "cache-mode"):
		return "memory"
	case n == "save", n == "dir", has("dbfilename", "rdb", "appendonly", "appendfsync", "appendfilename",
		"appenddirname", "aof", "no-appendfsync", "auto-aof", "stop-writes-on-bgsave", "snapshot", "df-snapshot"):
		return "persistence"
	case has("repl", "replica", "slave", "min-replicas", "min-slaves", "masterhost", "masterport"):
		return "replication"
	case has("bind", "port", "tcp-", "timeout", "unixsocket", "maxclients", "io-threads", "socket-mark"):
		return "network"
	case has("client-output-buffer", "client-query-buffer", "proto-max", "hz", "dynamic-hz",
		"max-new", "tracking-table"):
		return "limits"
	case has("loglevel", "logfile", "syslog", "slowlog", "latency", "crash", "verbosity", "hide-user-data"):
		return "logging"
	case has("hash-max", "list-max", "list-compress", "set-max", "zset-max", "stream-node", "hll-"):
		return "data types"
	case has("lua-", "busy-reply", "script"):
		return "scripting"
	case has("cluster"):
		return "cluster"
	case has("databases", "daemonize", "pidfile", "supervised", "always-show-logo", "notify-keyspace",
		"set-proc-title", "proc-title", "locale", "oom-score"):
		return "general"
	}
	return "other"
}

// RedisConfigRead reads the whole configuration, grouped, with every
// credential withheld.
func RedisConfigRead(ctx context.Context, client *redis.Client, profile *RedisProfile) (*RedisConfig, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	out := &RedisConfig{Groups: []RedisConfigGroup{}}
	if !profile.Features.Config {
		out.Reason = redisProductName(profile) + " does not offer CONFIG to this connection."
		return out, nil
	}
	pipe := client.Pipeline()
	all := redisConfigGet(ctx, pipe, "*")
	server := pipe.Info(ctx, "server")
	_, _ = pipe.Exec(ctx)
	if err := all.Err(); err != nil {
		out.Reason = "The server refused CONFIG GET: " + err.Error()
		return out, nil
	}
	values := redisConfigValues(all)
	out.Supported = true
	if server.Err() == nil {
		out.ConfigFile = redisInfoMap(parseRedisInfo(server.Val()))["config_file"]
	}
	out.Rewritable = out.ConfigFile != ""

	groups := map[string][]RedisConfigParam{}
	for name, value := range values {
		p := RedisConfigParam{Name: name, Value: value}
		if RedisConfigSecret(name) {
			set := value != ""
			p.Value, p.Secret, p.Set = "", true, &set
		}
		g := redisConfigGroup(name)
		groups[g] = append(groups[g], p)
	}
	for _, name := range redisConfigOrder {
		params := groups[name]
		if len(params) == 0 {
			continue
		}
		sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
		out.Groups = append(out.Groups, RedisConfigGroup{Name: name, Params: params})
	}
	return out, nil
}

// RedisConfigChange is what a CONFIG SET did.
type RedisConfigChange struct {
	Name   string `json:"name"`
	Secret bool   `json:"secret"`
	// Rewritten is whether the change was also written to the configuration
	// file. When it was asked for and could not be, RewriteError says why and
	// the change still stands until the next restart.
	Rewritten    bool   `json:"rewritten"`
	RewriteError string `json:"rewriteError,omitempty"`
	Notice       string `json:"notice,omitempty"`
}

var redisConfigNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// RedisConfigSet changes one parameter of the running server, and with
// rewrite also writes the configuration file so the change outlives a
// restart.
func RedisConfigSet(ctx context.Context, client *redis.Client, name, value string, rewrite bool) (*RedisConfigChange, error) {
	if !redisConfigNameRe.MatchString(name) {
		return nil, fmt.Errorf("that is not a configuration parameter name")
	}
	if len(value) > 64<<10 || strings.ContainsRune(value, 0) {
		return nil, fmt.Errorf("that value is not one a configuration parameter can hold")
	}
	change := &RedisConfigChange{Name: name, Secret: RedisConfigSecret(name)}
	if err := client.ConfigSet(ctx, name, value).Err(); err != nil {
		// The server quotes a value it will not take. For a password that
		// would put it in the error, and the error in the audit log.
		if change.Secret && value != "" && strings.Contains(err.Error(), value) {
			return nil, fmt.Errorf("the server refused that value for %s", name)
		}
		return nil, err
	}
	if redisConfigName(name) == "requirepass" {
		change.Notice = "The server's password changed. This connection still holds the old one; update it under Settings or the dashboard will lose access."
	}
	if rewrite {
		if err := client.ConfigRewrite(ctx).Err(); err != nil {
			change.RewriteError = err.Error()
		} else {
			change.Rewritten = true
		}
	}
	return change, nil
}

// RedisSlowEntry is one command the slow log kept.
type RedisSlowEntry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	DurationUs int64     `json:"durationUs"`
	// Command is the command's name; Args is all of it as the server kept
	// it, which abbreviates long arguments and long argument lists itself.
	Command    string       `json:"command"`
	Args       []RedisBytes `json:"args"`
	Client     string       `json:"client,omitempty"`
	ClientName string       `json:"clientName,omitempty"`
}

// RedisSlowlog is the slow log and the two settings that shape it.
type RedisSlowlog struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// ThresholdUs is slowlog-log-slower-than in microseconds: negative is
	// off, zero logs every command.
	ThresholdUs *int64 `json:"thresholdUs,omitempty"`
	// MaxLen is how many entries the server keeps before dropping the oldest.
	MaxLen *int64 `json:"maxLen,omitempty"`
	// Length is how many it holds now.
	Length  int64            `json:"length"`
	Entries []RedisSlowEntry `json:"entries"`
}

const redisSlowlogMax = 1024

// RedisSlowlogRead reads the newest count entries of the slow log.
func RedisSlowlogRead(ctx context.Context, client *redis.Client, profile *RedisProfile, count int) (*RedisSlowlog, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	out := &RedisSlowlog{Entries: []RedisSlowEntry{}}
	if !profile.Features.Slowlog {
		out.Reason = redisProductName(profile) + " has no slow log."
		return out, nil
	}
	if count <= 0 {
		count = 128
	}
	if count > redisSlowlogMax {
		count = redisSlowlogMax
	}
	pipe := client.Pipeline()
	get := pipe.Do(ctx, "SLOWLOG", "GET", count)
	length := pipe.Do(ctx, "SLOWLOG", "LEN")
	settings := map[string]*redis.Cmd{}
	if profile.Features.Config {
		for _, name := range []string{"slowlog-log-slower-than", "slowlog-max-len"} {
			settings[name] = redisConfigGet(ctx, pipe, name)
		}
	}
	_, _ = pipe.Exec(ctx)
	raw, err := get.Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	out.Supported = true
	out.Length, _ = length.Int64()
	for name, into := range map[string]**int64{
		"slowlog-log-slower-than": &out.ThresholdUs, "slowlog-max-len": &out.MaxLen,
	} {
		cmd, asked := settings[name]
		if !asked {
			continue
		}
		if v, ok := redisConfigValue(cmd, name); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				*into = &n
			}
		}
	}
	for _, item := range redisSlice(raw) {
		fields := redisSlice(item)
		if len(fields) < 4 {
			continue
		}
		e := RedisSlowEntry{Args: redisBytesList(redisStringList(fields[3]))}
		e.ID, _ = redisInt(fields[0])
		ts, _ := redisInt(fields[1])
		e.At = time.Unix(ts, 0).UTC()
		e.DurationUs, _ = redisInt(fields[2])
		if len(e.Args) > 0 {
			e.Command = strings.ToUpper(string(e.Args[0]))
		}
		if len(fields) > 4 {
			e.Client = redisText(fields[4])
		}
		if len(fields) > 5 {
			e.ClientName = redisText(fields[5])
		}
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}

// RedisSlowlogReset empties the slow log.
func RedisSlowlogReset(ctx context.Context, client *redis.Client) error {
	return client.Do(ctx, "SLOWLOG", "RESET").Err()
}

// RedisSaveModes are the two background writes an operator can ask for.
var RedisSaveModes = map[string]bool{"bgsave": true, "bgrewriteaof": true}

// RedisSave starts a background snapshot or a rewrite of the append-only
// file, and returns the server's own word on it. Both run in a child process
// and neither blocks clients; the foreground SAVE that does is not offered.
func RedisSave(ctx context.Context, client *redis.Client, profile *RedisProfile, mode string) (string, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return "", err
		}
	}
	switch mode {
	case "bgsave":
		return client.BgSave(ctx).Result()
	case "bgrewriteaof":
		if !profile.Features.AOF {
			return "", fmt.Errorf("%s has no append-only file to rewrite", redisProductName(profile))
		}
		return client.BgRewriteAOF(ctx).Result()
	}
	return "", fmt.Errorf("mode must be bgsave or bgrewriteaof")
}

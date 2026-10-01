package dbx

// What the Redis pages offer, by flavour, for the driver catalogue. Only
// flags no other engine has are registered here; the ones Redis shares with
// other engines are rows of the table in capabilities.go. A flag says the
// page exists for the flavour: whether one server has the command behind it
// is in that server's own `features`.
func init() {
	byFlavor := func(values map[string]any) CapabilityRule {
		return func(d Driver, flavor string) any {
			if v, ok := values[flavor]; ok && d == DriverRedis {
				return v
			}
			return false
		}
	}
	for _, flag := range []string{
		"keys", "keyTree", "keyTypeFilter", "keyMeta", "valueDownload", "bulkKeys", "streams",
		"logicalDatabases", "consoleClassify", "serverInfo", "commandStats", "latency", "queryLogReset",
		"persistence", "memoryAnalysis", "aclRules", "pubsub", "pubsubLive", "monitor",
	} {
		RegisterCapabilities(Capability{Flag: flag, Has: redisEngines})
	}
	RegisterCapabilities(
		// Dragonfly keeps neither an encoding nor an idle time per key, and
		// has no append-only file to rewrite.
		Capability{Flag: "keyEncoding", Has: redisEngines.Except(FlavorDragonfly)},
		Capability{Flag: "aofRewrite", Has: redisEngines.Except(FlavorDragonfly)},
		// JSON values: a module on Redis and Valkey, built in on Dragonfly.
		Capability{Flag: "json", Has: byFlavor(map[string]any{FlavorRedis: "module", FlavorValkey: "module", FlavorDragonfly: true})},
		// An expiry on one field of a hash, and the release that brought it.
		Capability{Flag: "hashFieldTtl", Has: byFlavor(map[string]any{FlavorRedis: "7.4+", FlavorValkey: "9.0+"})},
		// What the command reference holds: the server's own documentation,
		// or only the names of its commands.
		Capability{Flag: "commandReference", Has: byFlavor(map[string]any{
			FlavorRedis: "docs", FlavorValkey: "docs", FlavorKeyDB: "names", FlavorDragonfly: "names"})},
	)
}

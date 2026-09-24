package deploy

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Database detection beyond the Node, Python and Go driver tables: the
// manifests of every other ecosystem the detector reads, the connection
// string shape each consumer parses, the extensions a schema needs from the
// engine, and the drivers that only speak a hosted provider's protocol.

type databaseEvidence struct{ engine, evidence string }

var (
	gemDatabaseDrivers  = []struct{ name, engine string }{{"pg", "postgres"}, {"mysql2", "mysql"}, {"trilogy", "mysql"}, {"redis", "redis"}, {"sidekiq", "redis"}, {"mongoid", "mongodb"}}
	mixDatabaseDrivers  = []struct{ name, engine string }{{"postgrex", "postgres"}, {"myxql", "mysql"}, {"redix", "redis"}, {"mongodb_driver", "mongodb"}}
	cargoDatabaseCrates = []struct{ name, engine string }{
		{"tokio-postgres", "postgres"}, {"deadpool-postgres", "postgres"}, {"bb8-postgres", "postgres"}, {"postgres", "postgres"},
		{"mysql_async", "mysql"}, {"mysql", "mysql"}, {"redis", "redis"}, {"deadpool-redis", "redis"}, {"bb8-redis", "redis"}, {"fred", "redis"},
		{"mongodb", "mongodb"},
	}
	cargoFeatureCrates = []struct{ crate, feature, engine string }{
		{"sqlx", "postgres", "postgres"}, {"sqlx", "mysql", "mysql"}, {"diesel", "postgres", "postgres"}, {"diesel", "mysql", "mysql"},
		{"sea-orm", "sqlx-postgres", "postgres"}, {"sea-orm", "sqlx-mysql", "mysql"},
	}
	jvmDatabaseDrivers = []struct{ artifact, engine string }{
		{"org.postgresql", "postgres"}, {"quarkus-jdbc-postgresql", "postgres"}, {"quarkus-reactive-pg-client", "postgres"},
		{"mysql-connector-j", "mysql"}, {"mysql-connector-java", "mysql"}, {"quarkus-jdbc-mysql", "mysql"},
		{"mariadb-java-client", "mariadb"}, {"quarkus-jdbc-mariadb", "mariadb"},
		{"spring-boot-starter-data-redis", "redis"}, {"jedis", "redis"}, {"lettuce-core", "redis"}, {"quarkus-redis-client", "redis"},
		{"spring-boot-starter-data-mongodb", "mongodb"}, {"mongodb-driver", "mongodb"}, {"quarkus-mongodb-client", "mongodb"},
	}
	nugetDatabaseDrivers = []struct{ prefix, engine string }{
		{"npgsql", "postgres"}, {"pomelo.entityframeworkcore.mysql", "mysql"}, {"mysqlconnector", "mysql"}, {"mysql.data", "mysql"},
		{"mysql.entityframeworkcore", "mysql"}, {"stackexchange.redis", "redis"}, {"microsoft.extensions.caching.stackexchangeredis", "redis"},
		{"mongodb.driver", "mongodb"}, {"mongodb.entityframeworkcore", "mongodb"},
	}
	swiftDatabaseDrivers = []struct{ name, engine string }{
		{"fluent-postgres-driver", "postgres"}, {"postgres-nio", "postgres"}, {"fluent-mysql-driver", "mysql"}, {"mysql-nio", "mysql"},
		{"fluent-mongo-driver", "mongodb"}, {"vapor/redis", "redis"}, {"redistack", "redis"},
	}
	dartDatabaseDrivers     = []struct{ name, engine string }{{"postgres", "postgres"}, {"mysql_client", "mysql"}, {"mysql1", "mysql"}, {"redis", "redis"}, {"mongo_dart", "mongodb"}}
	composerDatabaseDrivers = []struct{ name, engine string }{
		{"predis/predis", "redis"}, {"ext-redis", "redis"}, {"mongodb/laravel-mongodb", "mongodb"}, {"jenssegers/mongodb", "mongodb"},
		{"mongodb/mongodb", "mongodb"}, {"ext-mongodb", "mongodb"}, {"ext-pdo_pgsql", "postgres"}, {"ext-pgsql", "postgres"},
		{"ext-pdo_mysql", "mysql"}, {"ext-mysqli", "mysql"},
	}
	// extensionDependencies are the libraries that only work on a Postgres
	// that has the extension.
	extensionDependencies = []struct{ ecosystem, name, extension string }{
		{"node", "pgvector", "vector"}, {"python", "pgvector", "vector"}, {"python", "langchain-postgres", "vector"},
		{"python", "geoalchemy2", "postgis"}, {"gem", "neighbor", "vector"}, {"gem", "pgvector", "vector"},
		{"gem", "activerecord-postgis-adapter", "postgis"}, {"mix", "pgvector", "vector"}, {"mix", "geo_postgis", "postgis"},
		{"cargo", "pgvector", "vector"}, {"cargo", "postgis_diesel", "postgis"}, {"jvm", "com.pgvector", "vector"},
		{"jvm", "hibernate-vector", "vector"}, {"jvm", "hibernate-spatial", "postgis"}, {"jvm", "postgis-jdbc", "postgis"},
		{"nuget", "pgvector", "vector"}, {"nuget", "npgsql.nettopologysuite", "postgis"},
	}
	// hostedDependencies speak a provider's HTTP or WebSocket protocol, and
	// tcpDrivers are what make a hosted dependency just one option among
	// several rather than the only way the source connects.
	hostedDependencies = []struct{ name, hosted, engine string }{
		{"@neondatabase/serverless", "neon-http", "postgres"}, {"@vercel/postgres", "vercel-postgres", "postgres"},
		{"@prisma/extension-accelerate", "prisma-accelerate", "postgres"}, {"@planetscale/database", "planetscale-http", "mysql"},
		{"@upstash/redis", "upstash-rest", "redis"}, {"@vercel/kv", "upstash-rest", "redis"},
	}
	tcpDrivers = map[string][]string{
		"postgres": {"pg", "postgres", "pg-promise", "@prisma/client", "knex", "typeorm", "sequelize", "@mikro-orm/postgresql", "kysely"},
		"mysql":    {"mysql2", "mysql", "mariadb", "@prisma/client", "knex", "typeorm", "sequelize"},
		"redis":    {"ioredis", "redis", "bullmq", "connect-redis"},
	}
	dartDependencyRE    = regexp.MustCompile(`(?m)^  ([a-z0-9_]+):`)
	databaseYAMLRootRE  = regexp.MustCompile(`(?m)^production:\s*$`)
	databaseYAMLChildRE = regexp.MustCompile(`^  ([a-z_]+):\s*(?:&[A-Za-z0-9_]+)?\s*$`)
	composerMajorRE     = regexp.MustCompile(`(\d+)`)
	hostedEngines       = map[string]bool{"neon-http": true, "neon-ws": true, "vercel-postgres": true, "planetscale-http": true, "prisma-accelerate": true, "upstash-rest": true}
)

// manifestDatabaseEvidence reads the engines the root's other manifests name.
func manifestDatabaseEvidence(stack rootStack, observations []environmentObservation, variables []DetectedVariable) []databaseEvidence {
	found := []databaseEvidence{}
	at := func(name string) string { return path.Join(stack.root, name) }
	for _, driver := range gemDatabaseDrivers {
		if _, ok := stack.gems[driver.name]; ok {
			found = append(found, databaseEvidence{driver.engine, driver.name + " in " + at("Gemfile.lock")})
		}
	}
	for _, driver := range mixDatabaseDrivers {
		if stack.mix[driver.name] {
			found = append(found, databaseEvidence{driver.engine, driver.name + " in " + at("mix.exs")})
		}
	}
	if stack.cargo != "" {
		manifest := parseCargoManifest([]byte(stack.cargo))
		for _, crate := range cargoFeatureCrates {
			if manifest.deps[normalizeCrate(crate.crate)] && cargoFeatureEnabled(stack.cargo, crate.crate, crate.feature) {
				found = append(found, databaseEvidence{crate.engine, crate.crate + " " + crate.feature + " in " + at("Cargo.toml")})
			}
		}
		for _, crate := range cargoDatabaseCrates {
			if manifest.deps[normalizeCrate(crate.name)] {
				found = append(found, databaseEvidence{crate.engine, crate.name + " in " + at("Cargo.toml")})
			}
		}
	}
	if strings.TrimSpace(stack.jvm) != "" {
		for _, driver := range jvmDatabaseDrivers {
			if strings.Contains(stack.jvm, driver.artifact) {
				found = append(found, databaseEvidence{driver.engine, driver.artifact + " in the build manifest"})
			}
		}
	}
	packages := make([]string, 0, len(stack.nuget))
	for name := range stack.nuget {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	for _, driver := range nugetDatabaseDrivers {
		for _, name := range packages {
			if strings.HasPrefix(name, driver.prefix) {
				found = append(found, databaseEvidence{driver.engine, name + " package reference"})
				break
			}
		}
	}
	for _, driver := range swiftDatabaseDrivers {
		if strings.Contains(stack.swift, driver.name) {
			found = append(found, databaseEvidence{driver.engine, driver.name + " in " + at("Package.swift")})
		}
	}
	if stack.dart != "" {
		declared := map[string]bool{}
		for _, match := range dartDependencyRE.FindAllStringSubmatch(stack.dart, -1) {
			declared[match[1]] = true
		}
		for _, driver := range dartDatabaseDrivers {
			if declared[driver.name] {
				found = append(found, databaseEvidence{driver.engine, driver.name + " in " + at("pubspec.yaml")})
			}
		}
	}
	for _, driver := range composerDatabaseDrivers {
		if stack.composer.has(driver.name) {
			found = append(found, databaseEvidence{driver.engine, driver.name + " in " + at("composer.json")})
		}
	}
	for _, observation := range observed(observations, observeCommittedEngine) {
		engine, name, _ := strings.Cut(observation.detail, "|")
		found = append(found, databaseEvidence{engine, name + " scheme in " + observation.source})
	}
	for _, hosted := range hostedDependencies {
		if stack.nodeHas(hosted.name) != "" {
			found = append(found, databaseEvidence{hosted.engine, hosted.name + " in " + at("package.json")})
		}
	}
	return found
}

// cargoFeatureEnabled reads whether a crate's features list names a feature,
// in the inline table or the [dependencies.crate] form.
func cargoFeatureEnabled(manifest, crate, feature string) bool {
	quoted := regexp.QuoteMeta(crate)
	for _, expression := range []string{
		`(?m)^\s*` + quoted + `\s*=\s*\{[^}]*features\s*=\s*\[([^\]]*)\]`,
		`(?s)\[dependencies\.` + quoted + `\][^\[]*?features\s*=\s*\[([^\]]*)\]`,
	} {
		for _, match := range regexp.MustCompile(expression).FindAllStringSubmatch(manifest, -1) {
			if strings.Contains(match[1], `"`+feature+`"`) {
				return true
			}
		}
	}
	return false
}

// databaseExtensions lists the Postgres extensions a root's schema or
// libraries need.
func databaseExtensions(stack rootStack, observations []environmentObservation) []string {
	needed := map[string]bool{}
	for _, observation := range observed(observations, observeExtension) {
		if observation.detail == "vector" || observation.detail == "postgis" {
			needed[observation.detail] = true
		}
	}
	for _, dependency := range extensionDependencies {
		present := false
		switch dependency.ecosystem {
		case "node":
			present = stack.nodeHas(dependency.name) != ""
		case "python":
			present = stack.pythonHas(dependency.name) != ""
		case "gem":
			present = stack.gemHas(dependency.name) != ""
		case "mix":
			present = stack.mix[dependency.name]
		case "cargo":
			present = strings.Contains(stack.cargo, dependency.name)
		case "jvm":
			present = strings.Contains(stack.jvm, dependency.name)
		case "nuget":
			present = stack.nuget[dependency.name]
		}
		if present {
			needed[dependency.extension] = true
		}
	}
	result := make([]string, 0, len(needed))
	for extension := range needed {
		result = append(result, extension)
	}
	sort.Strings(result)
	return result
}

// enrichDatabases adds what the suggestion needs to wire the right thing:
// the connection string shape, the extensions, whether only the hosted
// provider can serve the driver, and the further databases Rails reads.
func enrichDatabases(stack rootStack, databases []DetectedDatabase, variables []DetectedVariable, observations []environmentObservation) []DetectedDatabase {
	extensions := databaseExtensions(stack, observations)
	hosted := hostedDrivers(stack, observations, variables)
	multi := railsDatabaseNames(stack)
	if len(extensions) > 0 && !slices.ContainsFunc(databases, func(database DetectedDatabase) bool { return database.Engine == "postgres" }) {
		// Only PostgreSQL has these extensions, so the library that needs one
		// names the engine even where no driver or URL does.
		noun := " extension"
		if len(extensions) > 1 {
			noun = " extensions"
		}
		databases = append(databases, DetectedDatabase{Engine: "postgres", Variable: databaseVariableNames["postgres"],
			Evidence: "the schema uses the " + strings.Join(extensions, " and ") + noun})
	}
	for index := range databases {
		database := &databases[index]
		relational := database.Engine == "postgres" || database.Engine == "mysql" || database.Engine == "mariadb"
		if database.Engine == "postgres" && len(extensions) > 0 {
			database.Extensions = extensions
		}
		if provider := hosted[database.Engine]; provider != "" {
			database.Hosted = provider
			if provider == "upstash-rest" {
				database.Variable = "UPSTASH_REDIS_REST_URL"
				for _, variable := range variables {
					if variable.Name == "KV_REST_API_URL" || variable.Name == "UPSTASH_REDIS_REST_URL" {
						database.Variable = variable.Name
						break
					}
				}
			}
		}
		if !relational || database.Hosted != "" {
			continue
		}
		// MariaDB Connector/J 3 accepts only its own scheme, and quick setup's
		// MariaDB records a mysql:// address, so the driver picks the form.
		jdbc := "jdbc"
		if database.Engine == "mariadb" {
			jdbc = "jdbc-mariadb"
		}
		switch {
		case strings.Contains(stack.jvm, "spring-boot") || strings.Contains(stack.jvm, "org.springframework.boot"):
			database.Format = jdbc
			database.Variable = "SPRING_DATASOURCE_URL"
			if datasource := observed(observations, observeSpringDatasource); len(datasource) > 0 {
				database.Variable = datasource[0].detail
			}
		case strings.Contains(stack.jvm, "io.quarkus"):
			database.Format, database.Variable = jdbc, "QUARKUS_DATASOURCE_JDBC_URL"
			if datasource := observed(observations, observeSpringDatasource); len(datasource) > 0 {
				database.Variable = datasource[0].detail
			}
		case strings.Contains(stack.jvm, "io.micronaut"):
			database.Format, database.Variable = jdbc, "DATASOURCES_DEFAULT_URL"
		case len(stack.nuget) > 0:
			database.Format = "adonet"
			database.Variable = "CONNECTIONSTRINGS__DEFAULTCONNECTION"
			if names := observed(observations, observeConnectionName); len(names) > 0 {
				database.Variable = "CONNECTIONSTRINGS__" + strings.ToUpper(names[0].detail)
			}
		case stack.rails() && (database.Engine == "mysql" || database.Engine == "mariadb") && stack.railsBefore72():
			database.Format = "mysql2"
		}
		if stack.rails() && database.Variable == "DATABASE_URL" {
			for _, name := range multi {
				if name != "primary" {
					database.AlsoVariables = append(database.AlsoVariables, strings.ToUpper(name)+"_DATABASE_URL")
				}
			}
			if len(database.AlsoVariables) > 4 {
				database.AlsoVariables = database.AlsoVariables[:4]
			}
		}
	}
	if len(databases) > 8 {
		databases = databases[:8]
	}
	return databases
}

// hostedDrivers names, per engine, the hosted protocol a root's driver
// speaks. An import seen in the source is conclusive; a dependency alone is
// only when no ordinary TCP driver is also declared, since then there is no
// other way for the application to connect.
func hostedDrivers(stack rootStack, observations []environmentObservation, variables []DetectedVariable) map[string]string {
	result := map[string]string{}
	engineOf := map[string]string{"neon-http": "postgres", "neon-ws": "postgres", "vercel-postgres": "postgres",
		"prisma-accelerate": "postgres", "planetscale-http": "mysql", "upstash-rest": "redis"}
	for _, observation := range observed(observations, observeHostedDriver) {
		if engine := engineOf[observation.detail]; engine != "" && result[engine] == "" {
			result[engine] = observation.detail
		}
	}
	for _, dependency := range hostedDependencies {
		if result[dependency.engine] != "" || stack.nodeHas(dependency.name) == "" || stack.nodeHas(tcpDrivers[dependency.engine]...) != "" {
			continue
		}
		result[dependency.engine] = dependency.hosted
	}
	for _, variable := range variables {
		lower := strings.ToLower(variable.Example)
		if result["postgres"] == "" && (strings.HasPrefix(lower, "prisma://") || strings.HasPrefix(lower, "prisma+postgres://")) {
			result["postgres"] = "prisma-accelerate"
		}
	}
	return result
}

// railsDatabaseNames reads the production database configurations Rails 8
// declares: primary, cache, queue and cable each read their own URL.
func railsDatabaseNames(stack rootStack) []string {
	if !stack.rails() {
		return nil
	}
	content := string(stack.facts[path.Join(stack.root, "config/database.yml")])
	location := databaseYAMLRootRE.FindStringIndex(content)
	if location == nil {
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(content[location[1]:], "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
		if match := databaseYAMLChildRE.FindStringSubmatch(line); match != nil {
			names = append(names, match[1])
		}
	}
	if len(names) < 2 {
		return nil
	}
	return names
}

// laravelURLVariable is the variable a Laravel application reads its whole
// database URL from: DB_URL from Laravel 11, DATABASE_URL before it. The
// application's own config/database.php decides when it was read; the
// framework constraint otherwise.
func laravelURLVariable(marker *detectedMarkers, variables []DetectedVariable) string {
	reads := map[string]bool{}
	for _, variable := range variables {
		for _, source := range variable.Sources {
			if strings.HasSuffix(source, "config/database.php") {
				reads[variable.Name] = true
			}
		}
	}
	switch {
	case reads["DATABASE_URL"] && !reads["DB_URL"]:
		return "DATABASE_URL"
	case reads["DB_URL"]:
		return "DB_URL"
	}
	if manifest, ok := parseComposerManifest(marker.composerJSON); ok {
		if match := composerMajorRE.FindString(manifest.Require["laravel/framework"]); match != "" {
			if major, err := strconv.Atoi(match); err == nil && major <= 10 {
				return "DATABASE_URL"
			}
		}
	}
	return "DB_URL"
}

var (
	databaseFormats = map[string]bool{"": true, "jdbc": true, "jdbc-mariadb": true, "adonet": true, "mysql2": true}
)

func validateDetectedDatabaseDetails(database DetectedDatabase) error {
	if !databaseFormats[database.Format] || (database.Hosted != "" && !hostedEngines[database.Hosted]) ||
		len(database.Extensions) > 4 || len(database.AlsoVariables) > 4 {
		return fmt.Errorf("detected database details are malformed")
	}
	for _, extension := range database.Extensions {
		if extension != "vector" && extension != "postgis" {
			return fmt.Errorf("detected database extension is malformed")
		}
	}
	for _, name := range database.AlsoVariables {
		if ValidateEnvKey(name) != nil {
			return fmt.Errorf("detected database variable is malformed")
		}
	}
	return nil
}

// ConnectionStringForFormat renders an application database URL in the
// shape its consumer parses. A JDBC URL carries the credentials as query
// parameters, which pgJDBC, Connector/J and MariaDB Connector/J all read, and
// "jdbc-mariadb" writes MariaDB Connector/J's own scheme whatever the server;
// an ADO.NET string is Npgsql's or MySqlConnector's keyword form. database,
// when set, names another database on the same server.
func ConnectionStringForFormat(raw, format, database string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("the connection is not a URL")
	}
	if database != "" {
		parsed.Path = "/" + database
	}
	if format == "" || format == "url" {
		return parsed.String(), nil
	}
	scheme := strings.ToLower(parsed.Scheme)
	engine := map[string]string{"postgres": "postgresql", "postgresql": "postgresql", "mysql": "mysql", "mysql2": "mysql", "mariadb": "mariadb"}[scheme]
	if engine == "" {
		return "", fmt.Errorf("%s connections have no %s form", scheme, format)
	}
	user := parsed.User.Username()
	password, _ := parsed.User.Password()
	name := strings.TrimPrefix(parsed.Path, "/")
	port := parsed.Port()
	switch format {
	case "mysql2":
		if engine == "postgresql" {
			return "", fmt.Errorf("postgres connections have no mysql2 form")
		}
		parsed.Scheme = "mysql2"
		return parsed.String(), nil
	case "jdbc", "jdbc-mariadb":
		if format == "jdbc-mariadb" {
			if engine == "postgresql" {
				return "", fmt.Errorf("postgres connections have no jdbc-mariadb form")
			}
			engine = "mariadb"
		}
		query := parsed.Query()
		if user != "" {
			query.Set("user", user)
		}
		if password != "" {
			query.Set("password", password)
		}
		address := parsed.Hostname()
		if strings.Contains(address, ":") {
			address = "[" + address + "]"
		}
		if port != "" {
			address += ":" + port
		}
		result := "jdbc:" + engine + "://" + address + "/" + name
		if encoded := query.Encode(); encoded != "" {
			result += "?" + encoded
		}
		return result, nil
	case "adonet":
		if port == "" {
			port = map[string]string{"postgresql": "5432", "mysql": "3306", "mariadb": "3306"}[engine]
		}
		quote := func(value string) string {
			if strings.ContainsAny(value, `;="' `) {
				return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
			}
			return value
		}
		if engine == "postgresql" {
			return fmt.Sprintf("Host=%s;Port=%s;Database=%s;Username=%s;Password=%s",
				quote(parsed.Hostname()), port, quote(name), quote(user), quote(password)), nil
		}
		return fmt.Sprintf("Server=%s;Port=%s;Database=%s;User ID=%s;Password=%s",
			quote(parsed.Hostname()), port, quote(name), quote(user), quote(password)), nil
	}
	return "", fmt.Errorf("connection format %q is not supported", format)
}

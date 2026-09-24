package deploy

import (
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Compose file inside a Git repository is often not the deployment at all:
// Laravel Sail's builds from vendor/, which no checkout contains, and a
// Symfony or Next.js repository's compose.yaml runs only the Postgres its
// developers use. Reading which kind it is, as data, keeps such a file from
// becoming a candidate that ties the framework's own recipe and can never
// build.

type composeDetection struct {
	path     string
	services []composeDetectionService
}

type composeDetectionService struct {
	Name       string
	Image      string
	Builds     bool
	Context    string
	Dockerfile string
	Target     string
}

const (
	composeKindApplication    = "application"
	composeKindDevelopment    = "development"
	composeKindInfrastructure = "infrastructure"
)

func composeFileName(name string) bool {
	switch strings.ToLower(name) {
	case "compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml":
		return true
	}
	return false
}

// readComposeForDetection parses one Compose file leniently: detection wants
// the services' shape, and the strict analysis a Compose source gets still
// runs when the repository is deployed as one.
func readComposeForDetection(tree detectionTree, composePath string) (composeDetection, bool) {
	content, ok := tree.read(composePath, 256<<10)
	if !ok {
		return composeDetection{}, false
	}
	var root yaml.Node
	if yaml.Unmarshal(content, &root) != nil || countYAMLNodes(&root, 0) > 100_000 {
		return composeDetection{}, false
	}
	services := mappingValue(documentMapping(&root), "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return composeDetection{}, false
	}
	detection := composeDetection{path: composePath}
	directory := path.Dir(composePath)
	for index := 0; index+1 < len(services.Content) && len(detection.services) < 64; index += 2 {
		node := services.Content[index+1]
		service := composeDetectionService{Name: services.Content[index].Value, Image: scalarMappingValue(node, "image")}
		if build := mappingValue(node, "build"); build != nil {
			service.Builds = true
			contextValue, dockerfileValue := ".", "Dockerfile"
			switch build.Kind {
			case yaml.ScalarNode:
				contextValue = build.Value
			case yaml.MappingNode:
				if value := scalarMappingValue(build, "context"); value != "" {
					contextValue = value
				}
				if value := scalarMappingValue(build, "dockerfile"); value != "" {
					dockerfileValue = value
				}
				service.Target = scalarMappingValue(build, "target")
			}
			if !strings.Contains(contextValue, "$") && !strings.Contains(contextValue, "://") {
				context := path.Clean(path.Join(directory, contextValue))
				if context == "." {
					context = ""
				}
				if context == "" || safeRelativePath(context) {
					service.Context = context
					if !strings.Contains(dockerfileValue, "$") {
						service.Dockerfile = dockerfileValue
					}
				}
			}
		}
		detection.services = append(detection.services, service)
	}
	return detection, len(detection.services) > 0
}

// composeBackingEngines maps the images development stacks run beside an
// application to the engine each one is, or "" for a backing tool that is
// not a database (a mail catcher, an admin UI, object storage).
var composeBackingEngines = map[string]string{
	"postgres": "postgres", "postgresql": "postgres", "postgis": "postgres", "timescaledb": "postgres",
	"mysql": "mysql", "mysql-server": "mysql", "percona": "mysql", "percona-server": "mysql", "mariadb": "mariadb",
	"redis": "redis", "valkey": "redis", "redis-stack": "redis", "redis-stack-server": "redis", "keydb": "redis",
	"mongo": "mongodb", "mongodb": "mongodb",
	"mailpit": "", "mailhog": "", "mailcatcher": "", "smtp4dev": "", "adminer": "", "pgadmin4": "", "pgadmin": "",
	"minio": "", "rabbitmq": "", "meilisearch": "", "memcached": "", "elasticsearch": "", "opensearch": "",
	"typesense": "", "localstack": "", "standalone-chrome": "", "standalone-firefox": "", "selenium": "",
	"phpmyadmin": "", "mongo-express": "", "redis-commander": "", "soketi": "",
}

// composeImageFamily is an image's repository name without registry,
// namespace, tag or digest: "bitnami/postgresql:16" is "postgresql".
func composeImageFamily(image string) (string, bool) {
	// Symfony's template pins `postgres:${POSTGRES_VERSION:-16}-alpine`: an
	// interpolated tag still names the image; an interpolated name does not.
	image = composeInterpolationRE.ReplaceAllString(image, "0")
	if image == "" || strings.Contains(image, "$") {
		return "", false
	}
	name, _, _ := strings.Cut(image, "@")
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		if colon := strings.LastIndex(name[slash:], ":"); colon >= 0 {
			name = name[:slash+colon]
		}
		name = name[strings.LastIndex(name, "/")+1:]
	} else {
		name, _, _ = strings.Cut(name, ":")
	}
	name = strings.ToLower(name)
	_, known := composeBackingEngines[name]
	return name, known
}

// classifyCompose reads what a repository's Compose files are for.
//
//   - infrastructure: every service is a backing image; there is no
//     application in it to deploy.
//   - development: every service that builds does so from a path the
//     checkout lacks (vendor/, a missing directory), beside backing images.
//   - application: anything else, a stack that is itself the deployment.
func classifyCompose(tree detectionTree, files []composeDetection) (string, string) {
	builds, missing, backing, total := 0, 0, 0, 0
	sail := false
	missingContext := ""
	for _, file := range files {
		for _, service := range file.services {
			total++
			if service.Builds {
				builds++
				context := service.Context
				underDependencies := false
				for _, segment := range strings.Split(context, "/") {
					underDependencies = underDependencies || segment == "vendor" || segment == "node_modules"
				}
				if underDependencies || (context != "" && !tree.exists(context)) {
					missing++
					missingContext = rootLabel(context)
					sail = sail || strings.Contains(context, "laravel/sail")
				}
				continue
			}
			if _, known := composeImageFamily(service.Image); known {
				backing++
			}
		}
	}
	switch {
	case total == 0:
		return composeKindApplication, ""
	case builds == 0 && backing == total:
		return composeKindInfrastructure, "runs only backing services"
	case builds > 0 && missing == builds && builds+backing == total:
		if sail {
			return composeKindDevelopment, "development services for the Laravel application (Laravel Sail)"
		}
		return composeKindDevelopment, "builds only from " + missingContext + ", which the repository does not contain"
	}
	return composeKindApplication, ""
}

// composeBackingDatabases turns a development stack's database images into
// the same suggestions a dependency would make.
func composeBackingDatabases(files []composeDetection) []DetectedDatabase {
	result := []DetectedDatabase{}
	seen := map[string]bool{}
	for _, file := range files {
		for _, service := range file.services {
			if service.Builds {
				continue
			}
			family, known := composeImageFamily(service.Image)
			engine := composeBackingEngines[family]
			if !known || engine == "" || seen[engine] {
				continue
			}
			evidence := "image " + service.Image + " in " + file.path
			if len(evidence) > 512 || rejectPlanSecretLiteral("evidence", evidence) != nil {
				evidence = "service " + service.Name + " in " + file.path
			}
			seen[engine] = true
			result = append(result, DetectedDatabase{Engine: engine, Variable: databaseVariableNames[engine], Evidence: evidence})
		}
	}
	return result
}

// composeCandidate is the Compose candidate a directory's files make, and
// whether it only runs backing services — a candidate only when the
// repository has nothing else to deploy.
func composeCandidate(tree detectionTree, marker *detectedMarkers, files []composeDetection) (DetectedCandidate, bool) {
	evidence := make([]DetectionEvidence, 0, len(marker.compose))
	for _, composePath := range marker.compose {
		evidence = append(evidence, DetectionEvidence{Path: composePath, Reason: "Compose configuration"})
	}
	candidate := DetectedCandidate{
		Name: "Compose stack in " + rootLabel(marker.root), Profile: ProfileCompose,
		Confidence: ConfidenceHigh, Evidence: evidence,
		NeedsDecision: []string{"review services, storage, ports, and unsupported fields"},
	}
	if len(files) == len(marker.compose) {
		kind, reason := classifyCompose(tree, files)
		switch kind {
		case composeKindInfrastructure:
			candidate.Confidence = ConfidenceLow
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: files[0].path, Reason: reason + "; the repository has nothing else to deploy"})
			return newDetectedCandidate(marker.root, BuildCompose, candidate), true
		case composeKindDevelopment:
			candidate.Confidence = ConfidenceLow
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: files[0].path, Reason: reason})
		}
	}
	return newDetectedCandidate(marker.root, BuildCompose, candidate), false
}

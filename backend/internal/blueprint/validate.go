package blueprint

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var ErrInvalidBlueprint = errors.New("blueprint is invalid")

var (
	identifierRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	versionRE    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	// variableRE is exactly the deployment engine's own environment-key rule.
	// A blueprint must not be able to declare a name the engine will reject, and
	// must not be stricter than it either: Gitea's GITEA__server__ROOT_URL is a
	// real variable name.
	variableRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	reviewedAtRE   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	imageRE        = regexp.MustCompile(`^[a-z0-9]+([._\-/][a-z0-9]+)*(:[a-zA-Z0-9._-]+)?$`)
	digestRE       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	absolutePathRE = regexp.MustCompile(`^/[^\x00]*$`)
)

// secretLookalikes are the variable names a default value must never be given.
// A blueprint that ships a working password ships it to everyone who installs it.
var secretLookalikes = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "PRIVATE_KEY"}

// Validate applies the supply-chain policy. It runs when a blueprint is parsed,
// which means at package initialization for every built-in — a built-in that
// breaks a rule cannot reach a running dashboard.
func Validate(blueprint *Blueprint) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidBlueprint, blueprint.ID, fmt.Sprintf(format, args...))
	}
	if !identifierRE.MatchString(blueprint.ID) {
		return fmt.Errorf("%w: id %q is not a lowercase identifier", ErrInvalidBlueprint, blueprint.ID)
	}
	if !versionRE.MatchString(blueprint.Version) {
		return fail("version %q is not major.minor.patch", blueprint.Version)
	}
	if strings.TrimSpace(blueprint.Name) == "" || strings.TrimSpace(blueprint.Description) == "" {
		return fail("name and description are required")
	}
	if !validCategory(blueprint.Category) {
		return fail("category %q is not one of the reviewed categories", blueprint.Category)
	}
	if !validProfile(blueprint.Profile) {
		return fail("profile %q is not a supported workload profile", blueprint.Profile)
	}
	if err := validateHTTPSURL(blueprint.DocsURL); err != nil {
		return fail("documentation URL: %v", err)
	}
	if err := validateProvenance(blueprint.Provenance); err != nil {
		return fail("%v", err)
	}
	if err := validateAccess(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateImage(blueprint.Image); err != nil {
		return fail("%v", err)
	}
	if err := validateInputs(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateSecrets(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validatePorts(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateVolumes(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateResources(blueprint.Resources); err != nil {
		return fail("%v", err)
	}
	if err := validateChecks(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateOperations(blueprint.Operations); err != nil {
		return fail("%v", err)
	}
	if err := validateTemplatedInputs(blueprint); err != nil {
		return fail("%v", err)
	}
	if err := validateFiles(blueprint.Files); err != nil {
		return fail("%v", err)
	}
	if err := validateAutomation(blueprint.Automation); err != nil {
		return fail("%v", err)
	}
	if err := validateSecurity(blueprint.Security); err != nil {
		return fail("%v", err)
	}
	if blueprint.Update.Detector == "" {
		return fail("an update detector is required, even if it is \"none\"")
	}
	if blueprint.Image.TagPolicy == "mutable" && strings.TrimSpace(blueprint.Update.Notes) == "" {
		return fail("a mutable tag policy must explain itself in update notes")
	}
	if len(blueprint.Fixtures) == 0 {
		return fail("at least one render fixture is required")
	}
	return nil
}

func validCategory(category Category) bool {
	switch category {
	case CategoryHTTP, CategoryDatabase, CategoryTool, CategoryAutomation, CategoryGame:
		return true
	}
	return false
}

func validProfile(profile Profile) bool {
	switch profile {
	case ProfileWeb, ProfileDatabase, ProfileTool, ProfileWorker, ProfileGame, ProfileCompose:
		return true
	}
	return false
}

func validateHTTPSURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%q is not an https URL", raw)
	}
	return nil
}

func validateProvenance(provenance Provenance) error {
	if strings.TrimSpace(provenance.Maintainer) == "" || strings.TrimSpace(provenance.License) == "" {
		return errors.New("provenance needs a maintainer and a license")
	}
	if err := validateHTTPSURL(provenance.UpstreamURL); err != nil {
		return fmt.Errorf("upstream URL: %v", err)
	}
	if !reviewedAtRE.MatchString(provenance.ReviewedAt) {
		return fmt.Errorf("reviewedAt %q is not YYYY-MM-DD", provenance.ReviewedAt)
	}
	if !versionRE.MatchString(provenance.MinimumDashboard) {
		return fmt.Errorf("minimumDashboard %q is not major.minor.patch", provenance.MinimumDashboard)
	}
	return nil
}

// validateAccess enforces the promise the picker makes on every card: that
// the operator is told, before deploying, how they will get in afterwards. An
// image that only prints its first password into its own log is an image this
// catalogue has to give credentials to, not one it may ship with a shrug.
func validateAccess(blueprint *Blueprint) error {
	access := blueprint.Access
	switch access.Kind {
	case AccessSetup, AccessCredentials, AccessToken, AccessClient, AccessOpen:
	case AccessUnavailable:
		// The whole point of the vocabulary. An image whose first password
		// exists only in its own log, or is the same for everybody, cannot be
		// offered: there is no sign-in to promise, so the definition is
		// retired and says so.
		if strings.TrimSpace(blueprint.Retired) == "" {
			return errors.New("access.kind \"unavailable\" means the first credential cannot be handed over, so the definition must also be retired")
		}
	case "":
		return errors.New("access.kind is required: every blueprint says how the first sign-in works")
	default:
		return fmt.Errorf("access.kind %q is not one of setup, credentials, token, client, open", access.Kind)
	}
	if strings.TrimSpace(access.Note) == "" {
		return errors.New("access.note is required: the sentence the operator reads before deploying")
	}
	if access.Path != "" && !strings.HasPrefix(access.Path, "/") {
		return fmt.Errorf("access.path %q is not an absolute path", access.Path)
	}
	secrets := map[string]bool{}
	for _, secret := range blueprint.Secrets {
		secrets[secret.Variable] = true
	}
	inputVariables := map[string]bool{}
	for _, input := range blueprint.Inputs {
		if input.Variable != "" {
			inputVariables[input.Variable] = true
		}
	}
	if access.SecretVariable != "" && !secrets[access.SecretVariable] {
		return fmt.Errorf("access names %q as the credential, which is not a generated secret of this blueprint", access.SecretVariable)
	}
	if access.UsernameVariable != "" && !inputVariables[access.UsernameVariable] {
		return fmt.Errorf("access names %q as the account name, which no input writes", access.UsernameVariable)
	}
	if access.Username != "" && access.UsernameVariable != "" {
		return errors.New("access declares both a fixed account name and an input that chooses one")
	}
	switch access.Kind {
	case AccessCredentials:
		if access.SecretVariable == "" {
			return errors.New("a credentials sign-in must name the generated secret that is its password")
		}
		if access.Username == "" && access.UsernameVariable == "" {
			return errors.New("a credentials sign-in must name the account the password belongs to")
		}
	case AccessToken:
		if access.SecretVariable == "" {
			return errors.New("a token sign-in must name the generated secret that is the token")
		}
		if access.Username != "" || access.UsernameVariable != "" {
			return errors.New("a token sign-in has no account name")
		}
	case AccessClient:
		// The kind means "another program connects with the generated
		// credential". Without one there is no credential to hand over, and
		// the honest answer is that the workload is open.
		if access.SecretVariable == "" {
			return errors.New("a client credential must name the generated secret the connecting program uses")
		}
	case AccessSetup, AccessOpen, AccessUnavailable:
		// Nothing is handed over, so naming a credential here would describe a
		// sign-in that does not happen.
		if access.SecretVariable != "" || access.Username != "" || access.UsernameVariable != "" {
			return fmt.Errorf("a %q sign-in hands over no credential, so it must not name one", access.Kind)
		}
	}
	return nil
}

func validateImage(image Image) error {
	reference := image.Reference
	if digest, _, found := strings.Cut(reference, "@"); found {
		reference = digest
	}
	if !imageRE.MatchString(reference) {
		return fmt.Errorf("image reference %q is not a plain registry reference", image.Reference)
	}
	if _, digest, found := strings.Cut(image.Reference, "@"); found && !digestRE.MatchString(digest) {
		return fmt.Errorf("image digest %q is not a sha256 content digest", digest)
	}
	switch image.TagPolicy {
	case "pinned", "minor", "major", "mutable":
	default:
		return fmt.Errorf("tag policy %q is not one of pinned, minor, major, mutable", image.TagPolicy)
	}
	switch image.PullPolicy {
	case "", "missing", "always":
	default:
		return fmt.Errorf("pull policy %q is not one of missing, always", image.PullPolicy)
	}
	if len(image.Command) > 64 {
		return fmt.Errorf("image command has %d arguments; the limit is 64", len(image.Command))
	}
	for index, argument := range image.Command {
		if argument == "" || len(argument) > 512 || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("image command argument %d is empty, too long or contains control characters", index)
		}
	}
	return nil
}

func validateInputs(blueprint *Blueprint) error {
	seen := map[string]bool{}
	variables := map[string]bool{}
	for _, input := range blueprint.Inputs {
		if !identifierRE.MatchString(input.Name) {
			return fmt.Errorf("input name %q is not a lowercase identifier", input.Name)
		}
		if seen[input.Name] {
			return fmt.Errorf("input %q is declared twice", input.Name)
		}
		seen[input.Name] = true
		if strings.TrimSpace(input.Label) == "" {
			return fmt.Errorf("input %q has no label", input.Name)
		}
		if input.Variable != "" {
			if !variableRE.MatchString(input.Variable) {
				return fmt.Errorf("input %q maps to invalid variable %q", input.Name, input.Variable)
			}
			if variables[input.Variable] {
				return fmt.Errorf("variable %q is written by two inputs", input.Variable)
			}
			variables[input.Variable] = true
			if input.Default != "" && looksLikeSecret(input.Variable) {
				return fmt.Errorf("input %q ships a default for secret-shaped variable %q", input.Name, input.Variable)
			}
		}
		switch input.Kind {
		case InputText, InputDomain:
			if input.Pattern != "" {
				if _, err := regexp.Compile(input.Pattern); err != nil {
					return fmt.Errorf("input %q has an invalid pattern: %v", input.Name, err)
				}
			}
		case InputNumber, InputMemory:
			if input.Maximum > 0 && input.Minimum > input.Maximum {
				return fmt.Errorf("input %q has minimum above maximum", input.Name)
			}
			if input.Default != "" {
				if _, err := strconv.Atoi(input.Default); err != nil {
					return fmt.Errorf("input %q has a non-numeric default", input.Name)
				}
			}
		case InputBoolean:
			if input.Default != "" && input.Default != "true" && input.Default != "false" {
				return fmt.Errorf("input %q has a non-boolean default", input.Name)
			}
		case InputChoice:
			if len(input.Choices) == 0 {
				return fmt.Errorf("input %q is a choice with no options", input.Name)
			}
			options := map[string]bool{}
			for _, choice := range input.Choices {
				if choice.Value == "" || strings.TrimSpace(choice.Label) == "" {
					return fmt.Errorf("input %q has an unlabelled choice", input.Name)
				}
				if options[choice.Value] {
					return fmt.Errorf("input %q repeats choice %q", input.Name, choice.Value)
				}
				options[choice.Value] = true
				if choice.Image != "" {
					if err := validateImage(Image{Reference: choice.Image, TagPolicy: blueprint.Image.TagPolicy}); err != nil {
						return fmt.Errorf("input %q choice %q: %v", input.Name, choice.Value, err)
					}
				}
			}
			if input.Default != "" && !options[input.Default] {
				return fmt.Errorf("input %q defaults to an undeclared choice", input.Name)
			}
		case InputAccept:
			if err := validateHTTPSURL(input.AcceptURL); err != nil {
				return fmt.Errorf("input %q accepts nothing in particular: %v", input.Name, err)
			}
			// Render records an acceptance and moves on, so a variable declared
			// here is never emitted. Both Minecraft definitions declared EULA
			// this way and both images exited 1 on every start, because the
			// agreement the operator had just signed never reached them. An
			// image that wants the answer as environment takes it from a
			// startup operation, where it is visible in the rendered plan.
			if input.Variable != "" {
				return fmt.Errorf("input %q is an acceptance and cannot become variable %q; set it from a startup operation instead", input.Name, input.Variable)
			}
			if input.Default != "" {
				return fmt.Errorf("input %q pre-accepts an agreement", input.Name)
			}
			if !input.Required {
				return fmt.Errorf("input %q is an acceptance that is not required", input.Name)
			}
		default:
			return fmt.Errorf("input %q has unsupported kind %q", input.Name, input.Kind)
		}
	}
	return nil
}

func looksLikeSecret(variable string) bool {
	upper := strings.ToUpper(variable)
	for _, marker := range secretLookalikes {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func validateSecrets(blueprint *Blueprint) error {
	seen := map[string]bool{}
	for _, secret := range blueprint.Secrets {
		if !identifierRE.MatchString(secret.Name) {
			return fmt.Errorf("secret name %q is not a lowercase identifier", secret.Name)
		}
		if seen[secret.Name] {
			return fmt.Errorf("secret %q is declared twice", secret.Name)
		}
		seen[secret.Name] = true
		if !variableRE.MatchString(secret.Variable) {
			return fmt.Errorf("secret %q maps to invalid variable %q", secret.Name, secret.Variable)
		}
		if secret.Length < 24 || secret.Length > 128 {
			return fmt.Errorf("secret %q asks for %d characters; generated secrets are 24-128", secret.Name, secret.Length)
		}
		if strings.TrimSpace(secret.Label) == "" {
			return fmt.Errorf("secret %q has no label", secret.Name)
		}
	}
	for _, input := range blueprint.Inputs {
		for _, secret := range blueprint.Secrets {
			if input.Variable != "" && input.Variable == secret.Variable {
				return fmt.Errorf("variable %q is both an input and a generated secret", secret.Variable)
			}
		}
	}
	return nil
}

func validatePorts(blueprint *Blueprint) error {
	names := map[string]bool{}
	occupied := map[string]bool{}
	primaries := 0
	for _, port := range blueprint.Ports {
		if !identifierRE.MatchString(port.Name) {
			return fmt.Errorf("port name %q is not a lowercase identifier", port.Name)
		}
		if names[port.Name] {
			return fmt.Errorf("port %q is declared twice", port.Name)
		}
		names[port.Name] = true
		if port.Internal < 1 || port.Internal > 65535 {
			return fmt.Errorf("port %q uses out-of-range internal port %d", port.Name, port.Internal)
		}
		protocol := strings.ToLower(port.Protocol)
		if protocol != "tcp" && protocol != "udp" {
			return fmt.Errorf("port %q uses protocol %q", port.Name, port.Protocol)
		}
		key := fmt.Sprintf("%d/%s", port.Internal, protocol)
		if occupied[key] {
			return fmt.Errorf("two ports both claim %s", key)
		}
		occupied[key] = true
		switch port.Exposure {
		case "proxy", "direct", "internal":
		default:
			return fmt.Errorf("port %q uses exposure %q", port.Name, port.Exposure)
		}
		if port.Exposure == "proxy" && protocol != "tcp" {
			return fmt.Errorf("port %q asks the HTTP proxy to carry %s", port.Name, protocol)
		}
		if strings.TrimSpace(port.Purpose) == "" {
			return fmt.Errorf("port %q does not say what it is for", port.Name)
		}
		if port.Primary {
			primaries++
		}
	}
	if primaries > 1 {
		return errors.New("more than one port is marked primary")
	}
	// A database reachable from the internet by default is the single most
	// harmful thing this catalogue could ship.
	if blueprint.Profile == ProfileDatabase {
		for _, port := range blueprint.Ports {
			if port.Exposure != "internal" {
				return fmt.Errorf("database port %q is exposed as %q; databases stay internal", port.Name, port.Exposure)
			}
		}
	}
	return nil
}

func validateVolumes(blueprint *Blueprint) error {
	names := map[string]bool{}
	targets := map[string]bool{}
	data := false
	for _, volume := range blueprint.Volumes {
		if !identifierRE.MatchString(volume.Name) {
			return fmt.Errorf("volume name %q is not a lowercase identifier", volume.Name)
		}
		if names[volume.Name] {
			return fmt.Errorf("volume %q is declared twice", volume.Name)
		}
		names[volume.Name] = true
		if !absolutePathRE.MatchString(volume.Target) || strings.Contains(volume.Target, "..") {
			return fmt.Errorf("volume %q mounts at %q, which is not a contained absolute path", volume.Name, volume.Target)
		}
		if targets[volume.Target] {
			return fmt.Errorf("two volumes both mount at %q", volume.Target)
		}
		targets[volume.Target] = true
		if strings.TrimSpace(volume.Purpose) == "" {
			return fmt.Errorf("volume %q does not say what it holds", volume.Name)
		}
		if volume.Data {
			data = true
			if volume.ReadOnly {
				return fmt.Errorf("volume %q holds data and is mounted read-only", volume.Name)
			}
		}
	}
	// A stateful workload with nowhere to keep its state loses it on the first
	// redeploy, which is exactly the failure a catalogue must not ship.
	if (blueprint.Profile == ProfileDatabase || blueprint.Profile == ProfileGame) && !data {
		return errors.New("a database or game blueprint must declare a persistent data volume")
	}
	return nil
}

func validateResources(resources Resources) error {
	if resources.MemoryMB <= 0 {
		return errors.New("a recommended memory figure is required")
	}
	if resources.MinMemoryMB > resources.MemoryMB {
		return errors.New("minimum memory is above the recommendation")
	}
	if resources.CPUs < 0 {
		return errors.New("cpus cannot be negative")
	}
	return nil
}

func validateChecks(blueprint *Blueprint) error {
	names := map[string]bool{}
	ports := map[string]bool{}
	for _, port := range blueprint.Ports {
		ports[port.Name] = true
	}
	required := 0
	for _, check := range blueprint.Checks {
		if strings.TrimSpace(check.Name) == "" {
			return errors.New("a check has no name")
		}
		if names[check.Name] {
			return fmt.Errorf("check %q is declared twice", check.Name)
		}
		names[check.Name] = true
		switch check.Phase {
		case "readiness", "smoke", "public":
		default:
			return fmt.Errorf("check %q runs in unsupported phase %q", check.Name, check.Phase)
		}
		switch check.Kind {
		case CheckHTTP:
			if check.Path == "" || !strings.HasPrefix(check.Path, "/") {
				return fmt.Errorf("check %q needs an absolute HTTP path", check.Name)
			}
		case CheckTCP, CheckHandshake:
			if check.Port == "" {
				return fmt.Errorf("check %q does not name a port", check.Name)
			}
		case CheckCommand:
			if len(check.Command) == 0 {
				return fmt.Errorf("check %q has no command", check.Name)
			}
		case CheckDocker:
		default:
			return fmt.Errorf("check %q has unsupported kind %q", check.Name, check.Kind)
		}
		if check.Port != "" && !ports[check.Port] {
			return fmt.Errorf("check %q names undeclared port %q", check.Name, check.Port)
		}
		if check.Required {
			required++
		}
	}
	if required == 0 {
		return errors.New("at least one required readiness check is needed to tell started from working")
	}
	return nil
}

func validateOperations(operations Operations) error {
	for name, list := range map[string][]Operation{
		"install": operations.Install, "release": operations.Release,
		"startup": operations.Startup, "stop": operations.Stop,
	} {
		for _, operation := range list {
			if err := validateOperation(operation); err != nil {
				return fmt.Errorf("%s operation: %v", name, err)
			}
		}
	}
	return nil
}

func validateOperation(operation Operation) error {
	switch operation.Kind {
	case OperationSetVariable:
		if !variableRE.MatchString(operation.Name) {
			return fmt.Errorf("sets invalid variable %q", operation.Name)
		}
		if looksLikeSecret(operation.Name) && operation.Value != "" {
			return fmt.Errorf("sets a literal value for secret-shaped variable %q", operation.Name)
		}
	case OperationWriteFile:
		if !absolutePathRE.MatchString(operation.Path) || strings.Contains(operation.Path, "..") {
			return fmt.Errorf("writes to uncontained path %q", operation.Path)
		}
	case OperationConsole:
		if strings.TrimSpace(operation.Value) == "" {
			return errors.New("sends an empty console command")
		}
		if strings.ContainsAny(operation.Value, "\x00\r\n") {
			return errors.New("sends a console command containing a newline")
		}
	case OperationStopSignal:
		if !strings.HasPrefix(operation.Signal, "SIG") {
			return fmt.Errorf("uses signal %q", operation.Signal)
		}
	case OperationWaitForLog:
		if operation.Pattern == "" {
			return errors.New("waits for an empty log pattern")
		}
		if _, err := regexp.Compile(operation.Pattern); err != nil {
			return fmt.Errorf("waits for an invalid log pattern: %v", err)
		}
		if operation.TimeoutSeconds <= 0 || operation.TimeoutSeconds > 900 {
			return errors.New("waits for a log without a bounded timeout")
		}
	case OperationFetchArtifact:
		if err := validateHTTPSURL(operation.URL); err != nil {
			return fmt.Errorf("fetches %v", err)
		}
		if operation.MaxBytes <= 0 || operation.MaxBytes > 8<<30 {
			return errors.New("fetches an artifact without a size limit")
		}
		// Either the bytes are pinned by checksum, or the version is resolved
		// through a declared API and the resolved artifact is recorded on the
		// run. There is no third option where nobody knows what arrived.
		if operation.Checksum == "" && operation.Value == "" {
			return errors.New("fetches an artifact with neither a checksum nor a version source")
		}
		if operation.Checksum != "" && !digestRE.MatchString(operation.Checksum) {
			return fmt.Errorf("fetches an artifact with malformed checksum %q", operation.Checksum)
		}
	default:
		return fmt.Errorf("uses unsupported kind %q", operation.Kind)
	}
	return nil
}

// validateTemplatedInputs refuses an operation that templates an input the
// operator can leave blank.
//
// expand substitutes a declared-but-empty input with the empty string and does
// not complain, so `https://{{input.domain}}` on an optional domain renders the
// literal "https://" — which Grafana then keeps as its root URL and builds
// every share link from. An input that steers a variable is an input the
// blueprint has to insist on, or give a default to.
func validateTemplatedInputs(blueprint *Blueprint) error {
	declared := map[string]Input{}
	for _, input := range blueprint.Inputs {
		declared[input.Name] = input
	}
	for _, list := range [][]Operation{
		blueprint.Operations.Install, blueprint.Operations.Release,
		blueprint.Operations.Startup, blueprint.Operations.Stop,
	} {
		for _, operation := range list {
			for _, field := range []string{operation.Value, operation.Content, operation.URL} {
				for _, match := range placeholderRE.FindAllStringSubmatch(field, -1) {
					input, found := declared[match[1]]
					if !found {
						return fmt.Errorf("an operation templates undeclared input %q", match[1])
					}
					if !input.Required && input.Default == "" {
						return fmt.Errorf("an operation templates input %q, which is optional with no default, so it renders as an empty substitution", input.Name)
					}
				}
			}
		}
	}
	return nil
}

func validateFiles(files []ConfigFile) error {
	paths := map[string]bool{}
	for _, file := range files {
		if !absolutePathRE.MatchString(file.Path) || strings.Contains(file.Path, "..") {
			return fmt.Errorf("editable file %q is not a contained absolute path", file.Path)
		}
		if paths[file.Path] {
			return fmt.Errorf("editable file %q is declared twice", file.Path)
		}
		paths[file.Path] = true
		switch file.Format {
		case "properties", "yaml", "json", "toml", "raw":
		default:
			return fmt.Errorf("editable file %q uses unsupported format %q", file.Path, file.Format)
		}
		if len(file.Properties) > 0 && file.Format == "raw" {
			return fmt.Errorf("editable file %q claims structured properties in a raw file", file.Path)
		}
		keys := map[string]bool{}
		for _, property := range file.Properties {
			if strings.TrimSpace(property.Key) == "" || strings.TrimSpace(property.Label) == "" {
				return fmt.Errorf("editable file %q has an unlabelled property", file.Path)
			}
			if keys[property.Key] {
				return fmt.Errorf("editable file %q repeats property %q", file.Path, property.Key)
			}
			keys[property.Key] = true
			if property.Kind == InputChoice && len(property.Choices) == 0 {
				return fmt.Errorf("property %q is a choice with no options", property.Key)
			}
		}
	}
	return nil
}

var automationActions = map[string]bool{
	"broadcast": true, "save": true, "backup": true, "stop": true,
	"start": true, "restart": true, "update": true, "console": true,
}

func validateAutomation(automation []Automation) error {
	names := map[string]bool{}
	for _, preset := range automation {
		if strings.TrimSpace(preset.Name) == "" {
			return errors.New("an automation preset has no name")
		}
		if names[preset.Name] {
			return fmt.Errorf("automation preset %q is declared twice", preset.Name)
		}
		names[preset.Name] = true
		if len(strings.Fields(preset.Cron)) != 5 {
			return fmt.Errorf("automation preset %q has cron %q, which is not five fields", preset.Name, preset.Cron)
		}
		if len(preset.Actions) == 0 {
			return fmt.Errorf("automation preset %q does nothing", preset.Name)
		}
		for _, action := range preset.Actions {
			if !automationActions[action] {
				return fmt.Errorf("automation preset %q uses action %q, which is outside the closed set", preset.Name, action)
			}
		}
	}
	return nil
}

func validateSecurity(security Security) error {
	privileged := security.Privileged || security.HostNetwork ||
		len(security.Capabilities) > 0 || len(security.Devices) > 0
	if privileged && strings.TrimSpace(security.Reason) == "" {
		return errors.New("privileged features are declared without a reason")
	}
	for _, device := range security.Devices {
		if !absolutePathRE.MatchString(device) || strings.Contains(device, "..") {
			return fmt.Errorf("device %q is not a contained absolute path", device)
		}
	}
	return nil
}

package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Custom Dockerfiles keep the documented stance that they receive no
// automatic build values, with one exception the Dockerfile itself asks for:
// a browser-public variable (publicBuildVariable) that the operator set as a
// plain build-scoped variable and the Dockerfile declares with ARG. Such a value is compiled into public
// JavaScript by design, so passing it is not disclosure; without it the
// bundle silently ships an empty API URL. The executor hands Prepare only
// plain build variable names for a Dockerfile build, so a secret never
// qualifies. The value reaches buildx through its process environment
// (`--build-arg NAME`), never argv or the Dockerfile.

// dockerfileBuildInputs checks the configured stage exists and chooses the
// build arguments a Dockerfile build receives.
func dockerfileBuildInputs(content []byte, target string, plainBuildVariables []string) (string, []string, error) {
	model := modelDockerfile(content)
	if target != "" {
		found := false
		for _, stage := range model.stages {
			found = found || stage.Name == strings.ToLower(target)
		}
		if !found {
			return "", nil, fmt.Errorf("%w: build target %s is not a stage of the Dockerfile", ErrUnsupportedBuilder, target)
		}
	}
	declared := map[string]bool{}
	for _, arg := range model.args(target) {
		declared[arg.Name] = true
	}
	names := []string{}
	for _, name := range plainBuildVariables {
		if declared[name] && publicBuildVariable(name) && !reservedBuildArgName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.ToLower(target), names, nil
}

func dockerfileBuildArgValues(names []string, variables map[string]string) ([]BuildArgValue, error) {
	values := make([]BuildArgValue, 0, len(names))
	for _, name := range names {
		value, ok := variables[name]
		if !ok {
			return nil, fmt.Errorf("%w: build argument %s is unavailable", ErrArtifactMissing, name)
		}
		values = append(values, BuildArgValue{Name: name, Value: value})
	}
	return values, nil
}

// composeServiceBuildArgs resolves a service's `build.args` the way Compose
// would, against composeBuildArgValues: a literal stays, `${X:-d}` falls back
// to d, and a bare name takes the variable of that name when there is one.
func composeServiceBuildArgs(service ComposeServicePlan, variables map[string]string) ([]BuildArgValue, error) {
	values := []BuildArgValue{}
	for _, arg := range service.BuildArgs {
		if reservedBuildArgName(arg.Name) {
			return nil, fmt.Errorf("build argument %s is reserved for the builder itself", arg.Name)
		}
		if arg.FromEnvironment {
			if value, ok := variables[arg.Name]; ok {
				values = append(values, BuildArgValue{Name: arg.Name, Value: value})
			}
			continue
		}
		value, err := interpolateComposeValue(arg.Value, variables)
		if err != nil {
			return nil, err
		}
		values = append(values, BuildArgValue{Name: arg.Name, Value: value})
	}
	return values, nil
}

func composeServiceTarget(service ComposeServicePlan, variables map[string]string) (string, error) {
	target, err := interpolateComposeValue(service.BuildTarget, variables)
	if err != nil {
		return "", err
	}
	if target != "" && !dockerfileStageNameRE.MatchString(target) {
		return "", fmt.Errorf("build target %q is not a stage name", target)
	}
	return target, nil
}

func (e *NormalizedStepExecutor) plainBuildVariableNames(ctx context.Context, run EngineRun) ([]string, error) {
	if e.variables == nil {
		return nil, fmt.Errorf("%w: variable store is unavailable", ErrArtifactMissing)
	}
	values, err := e.variables.OpenRunScopedVariables(ctx, run.ID, run.EnvironmentID, "build")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, value := range values {
		if value.Sensitivity == "plain" {
			names = append(names, value.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// composeArgVariableNames are the variables a build argument's value reads:
// its own name when it takes its value from the environment, otherwise every
// name its expression interpolates.
func composeArgVariableNames(arg ComposeBuildArg) []string {
	if arg.FromEnvironment {
		return []string{arg.Name}
	}
	return composeExpressionNames(arg.Value)
}

func composeExpressionNames(expression string) []string {
	names := []string{}
	for _, match := range composeInterpolationRE.FindAllStringSubmatch(strings.ReplaceAll(expression, "$$", ""), -1) {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		names = append(names, name)
	}
	return uniqueSorted(names)
}

// composeBuildArgValues are the values a Compose file's build arguments and
// stage targets interpolate from: the plain runtime and build variables
// together — Compose reads one environment for the whole file, and the form
// plans a Compose file's variables for runtime — but never a secret, because
// a build argument stays in the image's history and a target in buildx's
// argv. An argument or target that reads a secret refuses the build, as
// preflight's compose_build_arg_secret said it would.
func composeBuildArgValues(compose *ComposeAnalysis, scopes ...[]ScopedVariableValue) (map[string]string, error) {
	values, secret := map[string]string{}, map[string]bool{}
	for _, scoped := range scopes {
		for _, variable := range scoped {
			if variable.Sensitivity == "plain" {
				values[variable.Name] = variable.Value
			} else {
				secret[variable.Name] = true
			}
		}
	}
	if compose == nil {
		return values, nil
	}
	for _, service := range compose.Services {
		for _, arg := range service.BuildArgs {
			for _, name := range composeArgVariableNames(arg) {
				if secret[name] {
					return nil, fmt.Errorf("%w: Compose service %s build argument %s reads secret variable %s, and a build argument stays in the image's history",
						ErrUnsupportedBuilder, service.Name, arg.Name, name)
				}
			}
		}
		for _, name := range composeExpressionNames(service.BuildTarget) {
			if secret[name] {
				return nil, fmt.Errorf("%w: Compose service %s build target reads secret variable %s", ErrUnsupportedBuilder, service.Name, name)
			}
		}
	}
	return values, nil
}

func (e *NormalizedStepExecutor) composeBuildValues(ctx context.Context, run EngineRun, compose *ComposeAnalysis) (map[string]string, error) {
	if e.variables == nil {
		return nil, fmt.Errorf("%w: variable store is unavailable", ErrArtifactMissing)
	}
	runtime, err := e.variables.OpenRunScopedVariables(ctx, run.ID, run.EnvironmentID, "runtime")
	if err != nil {
		return nil, err
	}
	build, err := e.variables.OpenRunScopedVariables(ctx, run.ID, run.EnvironmentID, "build")
	if err != nil {
		return nil, err
	}
	return composeBuildArgValues(compose, runtime, build)
}

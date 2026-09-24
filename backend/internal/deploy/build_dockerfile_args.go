package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Custom Dockerfiles keep the documented stance that they receive no
// automatic build values, with one exception the Dockerfile itself asks for:
// a browser-public variable (NEXT_PUBLIC_, VITE_, PUBLIC_, NUXT_PUBLIC_,
// REACT_APP_) that the operator set as a plain build-scoped variable and the
// Dockerfile declares with ARG. Such a value is compiled into public
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
// would, against the scoped build values: a literal stays, `${X:-d}` falls
// back to d, and a bare name takes the variable of that name when there is
// one.
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

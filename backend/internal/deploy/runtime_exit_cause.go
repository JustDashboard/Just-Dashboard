package deploy

// A start command that returns — `pm2 start`, `forever start`, a trailing
// `&`, a one-off script given as the start command — ends the container with
// exit code 0, and the restart policy starts it again until readiness gives
// up. The output of such a run is usually a line saying the application was
// started, which reads like success; the exit code is what says otherwise.

// startCommandExitedCause is the cause a single container that stopped with
// exit code 0 proves. Several containers are a Compose stack, where a service
// that finishes (a migration, a seed) exits 0 by design.
func startCommandExitedCause(containers []ContainerDiagnostics) *OutputCause {
	if len(containers) != 1 {
		return nil
	}
	container := containers[0]
	if container.OOMKilled || container.ExitCode != 0 {
		return nil
	}
	switch container.State {
	case "exited", "restarting", "dead":
		return &OutputCause{Code: "start_command_exited"}
	}
	return nil
}

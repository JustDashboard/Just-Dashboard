package deploy

import (
	"runtime"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// gunicorn and uvicorn run one worker unless told otherwise, so one slow
// request — a PDF export, a call to a language model — holds every other
// visitor. Both read WEB_CONCURRENCY for their default, the variable Heroku
// sizes to the dyno, and a recipe image whose start command leaves the count
// to it gets one here, sized to the container: gunicorn's 2×CPU+1, within a
// quarter gigabyte of memory per worker when the plan limits memory, and 2
// when it does not, since the server's memory is then shared with whatever
// else runs on it. An explicit variable always wins, and an application that
// loads a model is never given more than one (the recipe does not mark it).

const webConcurrencyWorkerMiB = 256

func webConcurrencyEnvironment(snapshot runtimeReleaseSnapshot, variables map[string]string) []dockerx.EnvVar {
	if _, explicit := variables["WEB_CONCURRENCY"]; explicit || !snapshot.WebConcurrency {
		return nil
	}
	return []dockerx.EnvVar{{Name: "WEB_CONCURRENCY", Value: strconv.Itoa(webConcurrency(snapshot.Plan, runtime.NumCPU()))}}
}

func webConcurrency(plan RuntimePlanConfig, hostCPUs int) int {
	cpus := float64(hostCPUs)
	if plan.CPUs > 0 && plan.CPUs < cpus {
		cpus = plan.CPUs
	}
	workers := int(2*cpus) + 1
	if plan.MemoryMB <= 0 {
		return min(workers, 2)
	}
	ceiling := int(plan.MemoryMB / webConcurrencyWorkerMiB)
	return max(1, min(workers, max(ceiling, 1)))
}

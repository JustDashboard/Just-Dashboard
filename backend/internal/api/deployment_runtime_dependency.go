package api

import (
	"context"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func (o *deploymentDependencyObserver) observeReservedRuntime(ctx context.Context, dependency deploy.PlannedDependency) deploy.DependencyObservation {
	return deploy.NewRuntimeReservationObserver(o.store, o.containers, o.native).ObserveRuntimeDependency(ctx, dependency)
}

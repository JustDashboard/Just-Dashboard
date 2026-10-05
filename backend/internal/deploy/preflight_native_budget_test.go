package deploy

import (
	"context"
	"errors"
	"testing"
	"time"
)

type nativeDependencyBudgetFixture struct {
	native  NativeBaselineObserver
	budget  time.Duration
	budgets map[string]time.Duration
	err     error
}

func (f *nativeDependencyBudgetFixture) ObserveDependencies(ctx context.Context, dependencies []PlannedDependency) ([]DependencyObservation, error) {
	if deadline, bounded := ctx.Deadline(); bounded {
		f.budget = time.Until(deadline)
	}
	if f.budgets == nil {
		f.budgets = map[string]time.Duration{}
	}
	f.budgets[dependencies[0].ResourceKind] = f.budget
	observation := f.native.ObserveNativeBaseline(ctx, ReleaseRuntime{Kind: "systemd", RuntimeID: dependencies[0].ResourceID})
	f.err = ctx.Err()
	return []DependencyObservation{{Kind: dependencies[0].Kind, ResourceKind: dependencies[0].ResourceKind, ResourceID: dependencies[0].ResourceID, Available: observation.Status == "available"}}, nil
}

func TestNativeReservationPreflightAllowsAuthorityVerificationBeyondOrdinaryDeadline(t *testing.T) {
	resource := &nativeDependencyBudgetFixture{native: &delayedNativeObservation{delay: 11 * time.Second}}
	observer := NewHostPreflightObserver(nil, "", nil).WithDependencies(resource)
	started := time.Now()
	observed, err := observer.Observe(t.Context(), ObservationRequest{Dependencies: []PlannedDependency{{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "systemd_unit", ResourceID: "owned-app.service"}}})
	if err != nil || len(observed.Dependencies) != 1 || !observed.Dependencies[0].Available || resource.err != nil {
		t.Fatalf("11-second native verification was cut off by the ordinary dependency deadline: %v %+v %v", err, observed.Dependencies, resource.err)
	}
	if time.Since(started) < 11*time.Second || resource.budget < 29*time.Second || resource.budget > 30*time.Second {
		t.Fatalf("native observation did not exercise its bounded 30-second budget: elapsed=%s budget=%s", time.Since(started), resource.budget)
	}
}

func TestNativeReservationPreflightKeepsOrdinaryAndCallerBounds(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		dependency PlannedDependency
		budget     time.Duration
	}{
		{"pm2 reservation", PlannedDependency{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "pm2_process"}, 30 * time.Second},
		{"docker reservation", PlannedDependency{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "docker_container"}, 10 * time.Second},
		{"ordinary linked resource", PlannedDependency{Kind: "storage", Ownership: OwnershipLinked, ResourceKind: "bind_mount"}, 10 * time.Second},
		{"unregistered native resource", PlannedDependency{Kind: "runtime", Ownership: OwnershipLinked, ResourceKind: "systemd_unit"}, 10 * time.Second},
		{"different resource kind", PlannedDependency{Kind: "service", Ownership: OwnershipManaged, ResourceKind: "systemd_unit"}, 10 * time.Second},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			resource := &nativeDependencyBudgetFixture{native: &delayedNativeObservation{}}
			observer := NewHostPreflightObserver(nil, "", nil).WithDependencies(resource)
			observed, err := observer.Observe(t.Context(), ObservationRequest{Dependencies: []PlannedDependency{fixture.dependency}})
			if err != nil || len(observed.Dependencies) != 1 || !observed.Dependencies[0].Available || resource.budget < fixture.budget-time.Second || resource.budget > fixture.budget {
				t.Fatalf("wrong dependency verification deadline: budget=%s wanted=%s error=%v", resource.budget, fixture.budget, err)
			}
		})
	}
	t.Run("mixed ordinary and native dependencies", func(t *testing.T) {
		resource := &nativeDependencyBudgetFixture{native: &delayedNativeObservation{}}
		observer := NewHostPreflightObserver(nil, "", nil).WithDependencies(resource)
		observed, err := observer.Observe(t.Context(), ObservationRequest{Dependencies: []PlannedDependency{
			{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "docker_container"},
			{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "systemd_unit"},
		}})
		if err != nil || len(observed.Dependencies) != 2 || resource.budgets["docker_container"] < 9*time.Second || resource.budgets["docker_container"] > 10*time.Second || resource.budgets["systemd_unit"] < 29*time.Second || resource.budgets["systemd_unit"] > 30*time.Second {
			t.Fatalf("native reservation extended an ordinary dependency deadline: budgets=%v error=%v", resource.budgets, err)
		}
	})
	t.Run("shorter caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()
		resource := &nativeDependencyBudgetFixture{native: &delayedNativeObservation{delay: 11 * time.Second}}
		observer := NewHostPreflightObserver(nil, "", nil).WithDependencies(resource)
		started := time.Now()
		observed, err := observer.Observe(ctx, ObservationRequest{Dependencies: []PlannedDependency{{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "systemd_unit"}}})
		if err != nil || len(observed.Dependencies) != 1 || observed.Dependencies[0].Available || !errors.Is(resource.err, context.DeadlineExceeded) || resource.budget > 40*time.Millisecond || time.Since(started) > time.Second {
			t.Fatalf("caller deadline was extended: elapsed=%s budget=%s error=%v parent=%v", time.Since(started), resource.budget, err, resource.err)
		}
	})
	t.Run("cancelled caller", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		resource := &nativeDependencyBudgetFixture{native: &delayedNativeObservation{delay: 11 * time.Second}}
		observer := NewHostPreflightObserver(nil, "", nil).WithDependencies(resource)
		started := time.Now()
		observed, err := observer.Observe(ctx, ObservationRequest{Dependencies: []PlannedDependency{{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "pm2_process"}}})
		if err != nil || len(observed.Dependencies) != 1 || observed.Dependencies[0].Available || !errors.Is(resource.err, context.Canceled) || time.Since(started) > time.Second {
			t.Fatalf("cancelled caller was ignored: elapsed=%s error=%v parent=%v", time.Since(started), err, resource.err)
		}
	})
}

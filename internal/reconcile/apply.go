package reconcile

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// ErrVerificationFailed identifies a post-apply mismatch between expected and actual routes.
var ErrVerificationFailed = errors.New("applied Tailscale routes did not match the expected configuration")

// Driver executes planned mutations and reads the resulting Tailscale routes.
type Driver interface {
	Apply(context.Context, Operation) error
	Routes(context.Context) ([]Route, error)
}

// ApplyResult is the outcome of executing a plan. Verified is set only after
// actual routes match the expected keys, targets, and Public values.
type ApplyResult struct {
	Completed []Operation
	Failed    *Operation
	Actual    []Route
	Verified  []Route
}

// Mismatch is one expected-versus-actual route difference.
type Mismatch struct {
	Key      string
	Expected Route
	Actual   Route
}

// VerificationError lists post-apply route mismatches.
type VerificationError struct {
	Mismatches []Mismatch
}

func (e *VerificationError) Error() string {
	return "Pier could not verify Tailscale routes after applying changes"
}

func (e *VerificationError) Unwrap() error {
	return ErrVerificationFailed
}

// Apply executes deletions, then updates, then creations, and verifies the result.
// Keep-only plans and plans with no mutations do not call the driver.
func Apply(ctx context.Context, plan Plan, driver Driver) (ApplyResult, error) {
	mutations := mutationOrder(plan.Operations)
	if len(mutations) == 0 {
		return ApplyResult{}, nil
	}

	var completed []Operation
	for _, op := range mutations {
		if err := driver.Apply(ctx, op); err != nil {
			failed := op
			actual, routesErr := driver.Routes(ctx)
			result := ApplyResult{
				Completed: append([]Operation(nil), completed...),
				Failed:    &failed,
				Actual:    actual,
			}
			if routesErr != nil {
				return result, errors.Join(err, fmt.Errorf("unable to re-read Tailscale routes: %w", routesErr))
			}
			return result, err
		}
		completed = append(completed, op)
	}

	actual, err := driver.Routes(ctx)
	if err != nil {
		return ApplyResult{Completed: completed}, fmt.Errorf("unable to read Tailscale routes after applying changes: %w", err)
	}

	mismatches := verify(plan.Operations, actual)
	if len(mismatches) > 0 {
		return ApplyResult{
			Completed: completed,
			Actual:    actual,
		}, &VerificationError{Mismatches: mismatches}
	}

	return ApplyResult{
		Completed: completed,
		Actual:    actual,
		Verified:  expectedRoutes(plan.Operations),
	}, nil
}

func mutationOrder(ops []Operation) []Operation {
	var deletes, updates, creates []Operation
	for _, op := range ops {
		switch op.Kind {
		case KindDelete:
			deletes = append(deletes, op)
		case KindUpdate:
			updates = append(updates, op)
		case KindCreate:
			creates = append(creates, op)
		}
	}
	ordered := make([]Operation, 0, len(deletes)+len(updates)+len(creates))
	ordered = append(ordered, deletes...)
	ordered = append(ordered, updates...)
	ordered = append(ordered, creates...)
	return ordered
}

func expectedRoutes(ops []Operation) []Route {
	byKey := make(map[string]Route)
	for _, op := range ops {
		switch op.Kind {
		case KindKeep, KindCreate, KindUpdate:
			byKey[op.After.Key()] = op.After
		}
	}
	return routesByKey(byKey)
}

func verify(ops []Operation, actual []Route) []Mismatch {
	actualByKey := indexRoutes(actual)
	expectedByKey := make(map[string]Route)
	deletedKeys := make(map[string]struct{})
	for _, op := range ops {
		switch op.Kind {
		case KindKeep, KindCreate, KindUpdate:
			expectedByKey[op.After.Key()] = op.After
		case KindDelete:
			deletedKeys[op.Before.Key()] = struct{}{}
		}
	}

	keys := make([]string, 0, len(expectedByKey)+len(deletedKeys))
	for key := range expectedByKey {
		keys = append(keys, key)
	}
	for key := range deletedKeys {
		if _, ok := expectedByKey[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var mismatches []Mismatch
	for _, key := range keys {
		want, hasExpected := expectedByKey[key]
		got, hasActual := actualByKey[key]
		if hasExpected {
			if !hasActual || got.Target != want.Target || got.Public != want.Public {
				mismatches = append(mismatches, Mismatch{Key: key, Expected: want, Actual: got})
			}
			continue
		}
		if hasActual {
			mismatches = append(mismatches, Mismatch{Key: key, Actual: got})
		}
	}
	return mismatches
}

func routesByKey(byKey map[string]Route) []Route {
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	routes := make([]Route, 0, len(keys))
	for _, key := range keys {
		routes = append(routes, byKey[key])
	}
	return routes
}

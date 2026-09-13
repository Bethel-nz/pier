// Package reconcile plans safe diffs between desired, actual, and owned routes.
package reconcile

import "sort"

// Build diffs desired, actual, and owned routes into a side-effect-free plan.
func Build(desired, actual, owned []Route, force bool) Plan {
	desiredByKey := indexRoutes(desired)
	actualByKey := indexRoutes(actual)
	ownedByKey := indexRoutes(owned)

	keys := make([]string, 0, len(desiredByKey)+len(actualByKey)+len(ownedByKey))
	seen := make(map[string]struct{}, cap(keys))
	for _, group := range []map[string]Route{desiredByKey, actualByKey, ownedByKey} {
		for key := range group {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var plan Plan
	for _, key := range keys {
		want, hasDesired := desiredByKey[key]
		got, hasActual := actualByKey[key]
		_, hasOwned := ownedByKey[key]

		switch {
		case hasDesired && !hasActual:
			plan.Operations = append(plan.Operations, Operation{Kind: KindCreate, After: want})
		case hasDesired && hasActual && sameValues(want, got):
			plan.Operations = append(plan.Operations, Operation{Kind: KindKeep, Before: got, After: want})
		case hasDesired && hasActual && hasOwned:
			plan.Operations = append(plan.Operations, Operation{Kind: KindUpdate, Before: got, After: want})
		case hasDesired && hasActual && force:
			plan.Operations = append(plan.Operations, Operation{Kind: KindUpdate, Before: got, After: want})
			plan.Conflicts = append(plan.Conflicts, Conflict{Desired: want, Actual: got, Takeover: true})
		case hasDesired && hasActual:
			plan.Conflicts = append(plan.Conflicts, Conflict{Desired: want, Actual: got})
		case hasActual && hasOwned:
			plan.Operations = append(plan.Operations, Operation{Kind: KindDelete, Before: got})
		}
	}
	return plan
}

func indexRoutes(routes []Route) map[string]Route {
	indexed := make(map[string]Route, len(routes))
	for _, route := range routes {
		indexed[route.Key()] = route
	}
	return indexed
}

func sameValues(desired, actual Route) bool {
	return desired.Target == actual.Target && desired.Public == actual.Public
}

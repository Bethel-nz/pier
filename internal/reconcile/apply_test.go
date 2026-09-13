package reconcile

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestApplyOrdersDeletesBeforeUpdatesAndCreates(t *testing.T) {
	create := Operation{Kind: KindCreate, After: Route{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true}}
	update := Operation{Kind: KindUpdate, Before: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}, After: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:8080"}}
	keep := Operation{Kind: KindKeep, Before: Route{HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}, After: Route{HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}}
	del := Operation{Kind: KindDelete, Before: Route{HTTPSPort: 8443, Path: "/old", Target: "http://127.0.0.1:9000"}}

	driver := &fakeDriver{routes: []Route{update.After, keep.After, create.After}}
	plan := Plan{Operations: []Operation{create, keep, update, del}}

	result, err := Apply(context.Background(), plan, driver)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	wantCalls := []Operation{del, update, create}
	if !reflect.DeepEqual(driver.applyCalls, wantCalls) {
		t.Errorf("Apply() mutations = %#v, want %#v", driver.applyCalls, wantCalls)
	}
	if !reflect.DeepEqual(result.Completed, wantCalls) {
		t.Errorf("Apply() completed = %#v, want %#v", result.Completed, wantCalls)
	}
	if result.Failed != nil {
		t.Errorf("Apply() failed = %#v, want nil", result.Failed)
	}
	if driver.routesCalls != 1 {
		t.Errorf("Apply() Routes() calls = %d, want 1", driver.routesCalls)
	}
}

func TestApplyPartialFailure(t *testing.T) {
	del := Operation{Kind: KindDelete, Before: Route{HTTPSPort: 8443, Path: "/old", Target: "http://127.0.0.1:9000"}}
	update := Operation{Kind: KindUpdate, Before: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}, After: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:8080"}}
	create := Operation{Kind: KindCreate, After: Route{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true}}
	applyErr := errors.New("tailscale funnel failed")
	actual := []Route{{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}}
	driver := &fakeDriver{
		failAt:   2,
		applyErr: applyErr,
		routes:   actual,
	}

	result, err := Apply(context.Background(), Plan{Operations: []Operation{del, update, create}}, driver)
	if !errors.Is(err, applyErr) {
		t.Fatalf("Apply() error = %v, want %v", err, applyErr)
	}
	if !reflect.DeepEqual(driver.applyCalls, []Operation{del, update}) {
		t.Errorf("Apply() mutations = %#v, want delete then failed update", driver.applyCalls)
	}
	if !reflect.DeepEqual(result.Completed, []Operation{del}) {
		t.Errorf("Apply() completed = %#v, want the successful delete", result.Completed)
	}
	if result.Failed == nil || !reflect.DeepEqual(*result.Failed, update) {
		t.Errorf("Apply() failed = %#v, want %#v", result.Failed, &update)
	}
	if !reflect.DeepEqual(result.Actual, actual) {
		t.Errorf("Apply() actual = %#v, want re-read routes %#v", result.Actual, actual)
	}
	if result.Verified != nil {
		t.Errorf("Apply() verified = %#v, want nil so unverified desired state is not saved", result.Verified)
	}
	if driver.routesCalls != 1 {
		t.Errorf("Apply() Routes() calls = %d, want 1 after the failure", driver.routesCalls)
	}
}

func TestApplyKeepOnlyDoesNotMutate(t *testing.T) {
	keep := Operation{
		Kind:   KindKeep,
		Before: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		After:  Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
	}
	driver := &fakeDriver{}

	result, err := Apply(context.Background(), Plan{Operations: []Operation{keep}}, driver)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(driver.applyCalls) != 0 {
		t.Errorf("Apply() mutations = %#v, want none", driver.applyCalls)
	}
	if driver.routesCalls != 0 {
		t.Errorf("Apply() Routes() calls = %d, want 0", driver.routesCalls)
	}
	if result.Verified != nil {
		t.Errorf("Apply() verified = %#v, want nil so unchanged ownership is retained", result.Verified)
	}
}

func TestApplyEmptyPlanDoesNotMutate(t *testing.T) {
	driver := &fakeDriver{}

	result, err := Apply(context.Background(), Plan{}, driver)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(driver.applyCalls) != 0 || driver.routesCalls != 0 {
		t.Errorf("Apply() invoked the driver: mutations=%#v routes=%d", driver.applyCalls, driver.routesCalls)
	}
	if result.Verified != nil {
		t.Errorf("Apply() verified = %#v, want nil", result.Verified)
	}
}

func TestApplyVerifiesExpectedRoutes(t *testing.T) {
	keep := Route{Service: "api", HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}
	created := Route{Service: "web", HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}
	updated := Route{Service: "hooks", HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true}
	driver := &fakeDriver{
		routes: []Route{
			{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
			{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
			{HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"},
			{HTTPSPort: 8443, Path: "/other", Target: "http://127.0.0.1:9999"},
		},
	}
	plan := Plan{Operations: []Operation{
		{Kind: KindCreate, After: created},
		{Kind: KindKeep, Before: keep, After: keep},
		{Kind: KindUpdate, Before: Route{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8000", Public: true}, After: updated},
		{Kind: KindDelete, Before: Route{HTTPSPort: 8443, Path: "/old", Target: "http://127.0.0.1:9000"}},
	}}

	result, err := Apply(context.Background(), plan, driver)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	wantVerified := []Route{updated, created, keep}
	if !reflect.DeepEqual(result.Verified, wantVerified) {
		t.Errorf("Apply() verified = %#v, want %#v", result.Verified, wantVerified)
	}
	if driver.routesCalls != 1 {
		t.Errorf("Apply() Routes() calls = %d, want 1 after mutations", driver.routesCalls)
	}
}

func TestApplyVerificationFailedPublic(t *testing.T) {
	want := Route{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true}
	got := Route{HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: false}
	driver := &fakeDriver{routes: []Route{got}}

	result, err := Apply(context.Background(), Plan{Operations: []Operation{{Kind: KindCreate, After: want}}}, driver)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Apply() error = %v, want ErrVerificationFailed", err)
	}
	var verifyErr *VerificationError
	if !errors.As(err, &verifyErr) {
		t.Fatalf("Apply() error type = %T, want *VerificationError", err)
	}
	wantMismatches := []Mismatch{{Key: want.Key(), Expected: want, Actual: got}}
	if !reflect.DeepEqual(verifyErr.Mismatches, wantMismatches) {
		t.Errorf("Apply() mismatches = %#v, want %#v", verifyErr.Mismatches, wantMismatches)
	}
	if result.Verified != nil {
		t.Errorf("Apply() verified = %#v, want nil when Public does not match", result.Verified)
	}
}

func TestApplyVerificationFailed(t *testing.T) {
	created := Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}
	deleted := Route{HTTPSPort: 8443, Path: "/old", Target: "http://127.0.0.1:9000"}
	driver := &fakeDriver{routes: []Route{
		{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:8080"},
		deleted,
	}}
	plan := Plan{Operations: []Operation{
		{Kind: KindCreate, After: created},
		{Kind: KindDelete, Before: deleted},
	}}

	result, err := Apply(context.Background(), plan, driver)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Apply() error = %v, want ErrVerificationFailed", err)
	}
	var verifyErr *VerificationError
	if !errors.As(err, &verifyErr) {
		t.Fatalf("Apply() error type = %T, want *VerificationError", err)
	}
	wantMismatches := []Mismatch{
		{Key: created.Key(), Expected: created, Actual: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:8080"}},
		{Key: deleted.Key(), Actual: deleted},
	}
	if !reflect.DeepEqual(verifyErr.Mismatches, wantMismatches) {
		t.Errorf("Apply() mismatches = %#v, want %#v", verifyErr.Mismatches, wantMismatches)
	}
	if result.Verified != nil {
		t.Errorf("Apply() verified = %#v, want nil when verification fails", result.Verified)
	}
	if !reflect.DeepEqual(result.Actual, driver.routes) {
		t.Errorf("Apply() actual = %#v, want re-read routes", result.Actual)
	}
}

type fakeDriver struct {
	applyCalls  []Operation
	applyErr    error
	failAt      int
	routes      []Route
	routesCalls int
}

func (f *fakeDriver) Apply(_ context.Context, op Operation) error {
	f.applyCalls = append(f.applyCalls, op)
	if f.failAt > 0 && len(f.applyCalls) == f.failAt {
		return f.applyErr
	}
	return nil
}

func (f *fakeDriver) Routes(context.Context) ([]Route, error) {
	f.routesCalls++
	return f.routes, nil
}

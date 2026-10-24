package biz

import "testing"

func TestModeldevTrainingOwnerIsFiniteAndExplicit(t *testing.T) {
	ref := &UsageRef{TenantID: NewID(), OwnerService: "ani-modeldev-service", ResourceID: NewID(), CreateOperationID: NewID()}
	if err := ValidateRef(ref); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"ani-modeldev", "ani-modeldev-service.other", "trainer", ""} {
		ref.OwnerService = owner
		if err := ValidateRef(ref); err == nil {
			t.Fatalf("unregistered owner %q accepted", owner)
		}
	}
}

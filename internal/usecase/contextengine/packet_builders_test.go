package contextengine

import "testing"

func TestPacketBuilderForRoleReturnsDedicatedBuilders(t *testing.T) {
	cases := []struct {
		role string
		ok   func(packetBuilder) bool
	}{
		{role: "planner", ok: func(builder packetBuilder) bool { _, ok := builder.(plannerPacketBuilder); return ok }},
		{role: "researcher", ok: func(builder packetBuilder) bool { _, ok := builder.(researcherPacketBuilder); return ok }},
		{role: "coder", ok: func(builder packetBuilder) bool { _, ok := builder.(coderPacketBuilder); return ok }},
		{role: "tester", ok: func(builder packetBuilder) bool { _, ok := builder.(testerPacketBuilder); return ok }},
		{role: "reviewer", ok: func(builder packetBuilder) bool { _, ok := builder.(reviewerPacketBuilder); return ok }},
		{role: "manager", ok: func(builder packetBuilder) bool { _, ok := builder.(finalizerPacketBuilder); return ok }},
		{role: "unknown", ok: func(builder packetBuilder) bool { _, ok := builder.(defaultPacketBuilder); return ok }},
	}

	for _, tc := range cases {
		builder := packetBuilderForRole(tc.role)
		if !tc.ok(builder) {
			t.Fatalf("unexpected builder type for role %q: %T", tc.role, builder)
		}
	}
}

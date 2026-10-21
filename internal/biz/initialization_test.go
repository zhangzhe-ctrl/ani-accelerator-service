package biz

import "testing"

func TestInitializationNormalizesReferencesBeforeWrites(t *testing.T) {
	input := InitializationInput{DiscoveryRevision: "revision", Pools: []InitializationPoolInput{{ClientKey: "z-pool", NewPool: &InitializationNewPool{DisplayName: "second"}}, {ClientKey: "a-pool", NewPool: &InitializationNewPool{DisplayName: "first"}}}, Groups: []InitializationGroupInput{{ClientKey: "group", NewGroup: &InitializationNewGroup{PoolKey: "a-pool", DisplayName: "group", BaselineRef: "baseline", QueueName: "default", Nodes: []NodeRef{{UID: "z-node", Name: "node-z"}, {UID: "a-node", Name: "node-a"}}}}}, Profiles: []InitializationProfileInput{{ClientKey: "profile", GroupKey: "group", DisplayName: "profile", MemoryMiBPerDevice: 8192, CoreLimitPercent: 25, MaxDevicesPerReplica: 1}}}
	normalized, err := NormalizeInitializationInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Pools[0].ClientKey != "a-pool" || normalized.Groups[0].NewGroup.Nodes[0].UID != "a-node" {
		t.Fatal("request was not canonically ordered")
	}
	if input.Pools[0].ClientKey != "z-pool" || input.Groups[0].NewGroup.Nodes[0].UID != "z-node" {
		t.Fatal("normalization mutated caller input")
	}
	poolOnly := InitializationInput{DiscoveryRevision: "revision", Pools: input.Pools}
	first, err := NormalizeInitializationInput(poolOnly)
	if err != nil {
		t.Fatal(err)
	}
	poolOnly.Groups = []InitializationGroupInput{}
	second, err := NormalizeInitializationInput(poolOnly)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := Hash(first)
	secondDigest, _ := Hash(second)
	if firstDigest != secondDigest {
		t.Fatal("equivalent empty lists changed idempotency digest")
	}
	for _, change := range []struct {
		name  string
		apply func(*InitializationInput)
	}{
		{name: "dangling_pool", apply: func(value *InitializationInput) { value.Groups[0].NewGroup.PoolKey = "missing" }},
		{name: "dangling_group", apply: func(value *InitializationInput) { value.Profiles[0].GroupKey = "missing" }},
		{name: "duplicate_key", apply: func(value *InitializationInput) { value.Pools[1].ClientKey = value.Pools[0].ClientKey }},
		{name: "invalid_oneof", apply: func(value *InitializationInput) { value.Pools[0].ExistingPoolID = NewID() }},
		{name: "node_overlap", apply: func(value *InitializationInput) {
			extra := *value.Groups[0].NewGroup
			value.Groups = append(value.Groups, InitializationGroupInput{ClientKey: "another", NewGroup: &extra})
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			copyValue, err := NormalizeInitializationInput(input)
			if err != nil {
				t.Fatal(err)
			}
			change.apply(&copyValue)
			if _, err = NormalizeInitializationInput(copyValue); err == nil {
				t.Fatal("invalid reference graph was accepted")
			}
		})
	}
}

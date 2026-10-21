package biz

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func nodeAllocationBacklogShape() []TelemetryAllocation {
	allocations := make([]TelemetryAllocation, 3866)
	for index := range allocations {
		allocations[index] = TelemetryAllocation{Namespace: "tenant-workloads", PodName: "existing-workload", ContainerName: "app", ResourceName: []string{"nvidia.com/gpumem", "nvidia.com/gpucores"}[index%2], DeviceIDs: []string{fmt.Sprintf("GPU-%036d/virtual-%03d", 3866-index, index)}}
	}
	allocations[0].DeviceIDs = []string{"last-device", "first-device"}
	return allocations
}

func priorTelemetryAllocationOrder(input []TelemetryAllocation) []TelemetryAllocation {
	output := append([]TelemetryAllocation(nil), input...)
	for index := range output {
		output[index].DeviceIDs = append([]string(nil), output[index].DeviceIDs...)
		sort.Strings(output[index].DeviceIDs)
	}
	sort.Slice(output, func(left, right int) bool {
		leftKey := output[left].Namespace + "\x00" + output[left].PodName + "\x00" + output[left].ContainerName + "\x00" + output[left].ResourceName
		rightKey := output[right].Namespace + "\x00" + output[right].PodName + "\x00" + output[right].ContainerName + "\x00" + output[right].ResourceName
		if leftKey == rightKey {
			leftHash, _ := Hash(output[left])
			rightHash, _ := Hash(output[right])
			return leftHash < rightHash
		}
		return leftKey < rightKey
	})
	return output
}

func TestNodeAllocationCanonicalOrderMatchesPriorContract(testContext *testing.T) {
	input := nodeAllocationBacklogShape()
	expected := priorTelemetryAllocationOrder(input)
	actual := canonicalTelemetryAllocations(input)
	if !reflect.DeepEqual(actual, expected) {
		testContext.Fatal("precomputed order changed the existing key/hash canonical contract")
	}
	expectedDigest, expectedErr := Hash(expected)
	actualDigest, actualErr := Hash(actual)
	if expectedErr != nil || actualErr != nil || actualDigest != expectedDigest {
		testContext.Fatal("allocation semantic digest changed")
	}
	if input[0].DeviceIDs[0] != "last-device" {
		testContext.Fatal("canonical ordering mutated original source facts")
	}
	if canonicalTelemetryAllocations(nil) != nil || canonicalTelemetryAllocations([]TelemetryAllocation{}) != nil {
		testContext.Fatal("canonical ordering changed the prior empty source shape")
	}
}

func BenchmarkNodeTelemetryAllocationOrder(benchmark *testing.B) {
	input := nodeAllocationBacklogShape()
	for _, variant := range []struct {
		name  string
		order func([]TelemetryAllocation) []TelemetryAllocation
	}{{name: "prior_comparator", order: priorTelemetryAllocationOrder}, {name: "precomputed", order: canonicalTelemetryAllocations}} {
		benchmark.Run(variant.name, func(benchmark *testing.B) {
			benchmark.ReportAllocs()
			for count := 0; count < benchmark.N; count++ {
				variant.order(input)
			}
		})
	}
}

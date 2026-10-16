package schedulerconfig

import (
	"fmt"
	"strings"
	"testing"
)

const protectedDocument = `actions: enqueue,allocate
tiers:
- plugins:
  - name: deviceshare
    arguments:
      deviceshare.VGPUEnable: true
      deviceshare.KnownGeometriesCMName: device-config
unknown:
  encoding: volcano-blocks
  scale: 1.5
`

func mustDigest(t *testing.T, version, document string) string {
	t.Helper()
	digest, err := ProtectedDigest(version, document)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestProtectedDigestReviewedScoringIsMutable(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2"} {
		t.Run(version, func(t *testing.T) {
			base := mustDigest(t, version, protectedDocument)
			for _, weight := range []string{"-2", "0", "3"} {
				for _, policy := range []string{"binpack", "spread", "''", "upstream-default-branch"} {
					document := strings.Replace(protectedDocument, "      deviceshare.VGPUEnable: true", "      deviceshare.VGPUEnable: true\n      deviceshare.SchedulePolicy: "+policy+"\n      deviceshare.ScheduleWeight: "+weight, 1)
					for key := range nodeorderWeights {
						document = strings.Replace(document, "unknown:", "  - name: nodeorder\n    enableNodeOrder: false\n    arguments:\n      "+key+": "+weight+"\nunknown:", 1)
					}
					document = strings.Replace(document, "unknown:", "  - name: binpack\n    arguments:\n      binpack.weight: "+weight+"\n      binpack.cpu: "+weight+"\n      binpack.memory: "+weight+"\n      binpack.resources: 'volcano.sh/vgpu-memory, nvidia.com/gpu'\n      binpack.resources.volcano.sh/vgpu-memory: "+weight+"\n      binpack.resources.nvidia.com/gpu: "+weight+"\nunknown:", 1)
					if got := mustDigest(t, version, document); got != base {
						t.Fatalf("reviewed scoring changed protected digest: weight=%s policy=%s", weight, policy)
					}
				}
			}
			without := "actions: enqueue,allocate\n"
			with := without + "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: 0\n"
			if mustDigest(t, version, with) != mustDigest(t, version, without) {
				t.Fatal("pure affinity scorer became mandatory")
			}
		})
	}
}

func TestProtectedDigestKeepsGPUAndUnknownContent(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2"} {
		base := mustDigest(t, version, protectedDocument)
		for name, document := range map[string]string{
			"mode":                    strings.Replace(protectedDocument, "VGPUEnable: true", "VGPUEnable: false", 1),
			"device-config-source":    strings.Replace(protectedDocument, "CMName: device-config", "CMName: other-config", 1),
			"encoding":                strings.Replace(protectedDocument, "volcano-blocks", "bytes", 1),
			"unit":                    strings.Replace(protectedDocument, "scale: 1.5", "scale: 2.5", 1),
			"actions-order":           strings.Replace(protectedDocument, "enqueue,allocate", "allocate,enqueue", 1),
			"unknown-scorer-argument": strings.Replace(protectedDocument, "unknown:", "  - name: nodeorder\n    arguments:\n      future-gpu-mode: true\nunknown:", 1),
			"unknown-device-argument": strings.Replace(protectedDocument, "VGPUEnable: true", "VGPUEnable: true\n      deviceshare.FutureEncoding: bytes", 1),
			"unknown-tier-field":      strings.Replace(protectedDocument, "- plugins:", "- future-gpu-field: 1\n  plugins:", 1),
			"other-hook":              strings.Replace(protectedDocument, "name: deviceshare", "name: deviceshare\n    enablePredicate: false", 1),
			"unlisted-binpack-key":    strings.Replace(protectedDocument, "unknown:", "  - name: binpack\n    arguments:\n      binpack.resources.unlisted: 1\nunknown:", 1),
		} {
			t.Run(version+"/"+name, func(t *testing.T) {
				if mustDigest(t, version, document) == base {
					t.Fatal("protected change was ignored")
				}
			})
		}
	}
}

func TestProtectedDigestRejectsMalformedDocumentsAndTypes(t *testing.T) {
	for name, document := range map[string]string{
		"empty": "", "yaml": "tiers: [", "scalar": "scalar", "second-document": "actions: allocate\n---\nactions: enqueue\n",
		"duplicate-root": "actions: allocate\nactions: enqueue\n", "duplicate-ignored-weight": "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: 1\n      podaffinity.weight: 2\n",
		"integer-key": "1: value\n", "tiers": "tiers: {}", "plugins": "tiers:\n- plugins: {}", "plugin": "tiers:\n- plugins:\n  - null", "arguments": "tiers:\n- plugins:\n  - name: nodeorder\n    arguments: []",
		"float-weight":    "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: 1.2",
		"string-weight":   "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: '2'",
		"overflow-weight": "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: 18446744073709551616",
		"null-weight":     "tiers:\n- plugins:\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: null",
		"policy":          "tiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.SchedulePolicy: true",
		"mode-type":       "tiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: 'true'",
		"scoring-hook":    "tiers:\n- plugins:\n  - name: nodeorder\n    enableNodeOrder: 'false'",
		"action-type":     "actions: []", "metrics-type": "metrics: []", "metrics-value": "metrics:\n  interval: 10", "configuration-type": "configurations: {}",
	} {
		t.Run(name, func(t *testing.T) {
			for _, version := range []string{"v1.12.0", "v1.15.2"} {
				if _, err := ProtectedDigest(version, document); err == nil {
					t.Fatal("invalid document accepted", version)
				}
			}
		})
	}
	if _, err := ProtectedDigest("v1.13.0", protectedDocument); err == nil {
		t.Fatal("unaudited version accepted")
	}
}

func TestProtectedDigestCanonicalYAMLSemantics(t *testing.T) {
	a := "actions: allocate\nfuture: {one: 1, two: true}\n"
	b := "# formatting only\nfuture:\n  two: true\n  one: 1\nactions: 'allocate'\n"
	if mustDigest(t, "v1.15.2", a) != mustDigest(t, "v1.15.2", b) {
		t.Fatal("mapping order or formatting changed protected semantics")
	}
	merged := "actions: allocate\nfuture:\n  <<: &defaults {two: true}\n  one: 1\n"
	if mustDigest(t, "v1.15.2", a) != mustDigest(t, "v1.15.2", merged) {
		t.Fatal("YAML merge lost protected values")
	}
	if mustDigest(t, "v1.12.0", a) == mustDigest(t, "v1.15.2", a) {
		t.Fatal("scheduler version is not committed")
	}
}

func TestConfigMapDigestProtectsOtherKeysAndBinaryData(t *testing.T) {
	base, err := ConfigMapDigest("v1.15.2", map[string]string{DataKey: protectedDocument, "other": "original"}, map[string]string{"binary": "AQ=="})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ data, binary map[string]string }{
		{map[string]string{DataKey: protectedDocument, "other": "changed"}, map[string]string{"binary": "AQ=="}},
		{map[string]string{DataKey: protectedDocument, "other": "original"}, map[string]string{"binary": "Ag=="}},
	} {
		got, err := ConfigMapDigest("v1.15.2", tc.data, tc.binary)
		if err != nil || got == base {
			t.Fatal("ConfigMap protected content lost", err)
		}
	}
	if _, err := ConfigMapDigest("v1.15.2", map[string]string{"other": "original"}, nil); err == nil {
		t.Fatal("missing scheduler document accepted")
	}
}

func TestProtectedDigestRejectsExcessiveAliases(t *testing.T) {
	document := "root: &level0 [value,value]\n"
	previous := "level0"
	for i := 1; i <= 20; i++ {
		name := fmt.Sprintf("level%d", i)
		document += name + ": &" + name + " [*" + previous + ",*" + previous + "]\n"
		previous = name
	}
	if _, err := ProtectedDigest("v1.15.2", document); err == nil {
		t.Fatal("excessive YAML aliases accepted")
	}
}

func TestProtectedDigestMatchesNativeYAML11BooleanTypes(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2"} {
		if mustDigest(t, version, "future: yes\n") == mustDigest(t, version, "future: 'yes'\n") {
			t.Fatal("native boolean/string change escaped unknown-field protection")
		}
		if mustDigest(t, version, protectedDocument) != mustDigest(t, version, strings.Replace(protectedDocument, "VGPUEnable: true", "VGPUEnable: yes", 1)) {
			t.Fatal("native YAML 1.1 boolean changed GPU mode")
		}
		badPolicy := "tiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.SchedulePolicy: on\n"
		if _, err := ProtectedDigest(version, badPolicy); err == nil {
			t.Fatal("native boolean was accepted as policy string")
		}
		if _, err := ProtectedDigest(version, strings.Replace(badPolicy, ": on", ": 'on'", 1)); err != nil {
			t.Fatal("native quoted policy string rejected", err)
		}
	}
}

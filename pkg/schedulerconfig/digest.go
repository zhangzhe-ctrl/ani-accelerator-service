// Package schedulerconfig defines the reviewed Volcano configuration projection
// shared by the supply provider and configuration editors. It does not confirm
// that a scheduler has loaded a ConfigMap or that a GPU baseline is verified.
package schedulerconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const DataKey = "volcano-scheduler.conf"

type scalar struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// ProtectedDigest validates one native scheduler document and hashes its
// protected projection. Only reviewed scoring settings are omitted. Version is
// explicit: a configuration document does not identify its scheduler binary.
func ProtectedDigest(schedulerVersion, document string) (string, error) {
	if schedulerVersion != "v1.12.0" && schedulerVersion != "v1.15.2" {
		return "", fmt.Errorf("unsupported Volcano version %q", schedulerVersion)
	}
	d := yaml.NewDecoder(strings.NewReader(document))
	var node yaml.Node
	if err := d.Decode(&node); err != nil {
		return "", fmt.Errorf("scheduler YAML: %w", err)
	}
	var extra yaml.Node
	if err := d.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("scheduler YAML must contain exactly one document")
	}
	if len(node.Content) != 1 {
		return "", fmt.Errorf("scheduler YAML document is empty")
	}
	// Keep yaml.v3's alias-expansion and duplicate-key guards before walking
	// nodes to retain unknown scalar tags in the protected projection.
	var checked any
	if err := node.Decode(&checked); err != nil {
		return "", fmt.Errorf("scheduler YAML: %w", err)
	}
	v, err := decode(node.Content[0], map[*yaml.Node]bool{})
	if err != nil {
		return "", err
	}
	root, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("scheduler document must be a mapping")
	}
	if err := project(root, schedulerVersion); err != nil {
		return "", err
	}
	return digest(struct {
		Version   string         `json:"version"`
		Protected map[string]any `json:"protected"`
	}{schedulerVersion, root})
}

// ConfigMapDigest is the scheduler baseline producer/consumer algorithm. Other
// data and all binaryData remain protected. binaryData uses the API's base64
// JSON representation; callers must not decode or omit it selectively.
func ConfigMapDigest(schedulerVersion string, data, binaryData map[string]string) (string, error) {
	document, ok := data[DataKey]
	if !ok {
		return "", fmt.Errorf("scheduler ConfigMap lacks %s", DataKey)
	}
	protected, err := ProtectedDigest(schedulerVersion, document)
	if err != nil {
		return "", err
	}
	copyData := make(map[string]string, len(data))
	for k, v := range data {
		copyData[k] = v
	}
	copyData[DataKey] = protected
	return digest(struct {
		Data       map[string]string `json:"data"`
		BinaryData map[string]string `json:"binaryData"`
	}{copyData, binaryData})
}

func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte("acc-scheduler-protected-v1\n"), b...))
	return hex.EncodeToString(h[:]), nil
}

func decode(n *yaml.Node, visiting map[*yaml.Node]bool) (any, error) {
	if visiting[n] {
		return nil, fmt.Errorf("cyclic YAML alias")
	}
	visiting[n] = true
	defer delete(visiting, n)
	switch n.Kind {
	case yaml.AliasNode:
		return decode(n.Alias, visiting)
	case yaml.ScalarNode:
		return nativeScalar(n), nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := decode(item, visiting)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		out, explicit := map[string]any{}, map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || (nativeScalar(key).Tag != "!!str" && key.Tag != "!!merge") {
				return nil, fmt.Errorf("YAML mapping keys must be strings")
			}
			if explicit[key.Value] {
				return nil, fmt.Errorf("duplicate YAML key %q", key.Value)
			}
			explicit[key.Value] = true
			v, err := decode(n.Content[i+1], visiting)
			if err != nil {
				return nil, err
			}
			if key.Tag == "!!merge" {
				merges, ok := v.([]any)
				if !ok {
					merges = []any{v}
				}
				for _, merge := range merges {
					m, ok := merge.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("YAML merge must contain mappings")
					}
					for k, x := range m {
						if _, exists := out[k]; !exists {
							out[k] = x
						}
					}
				}
			} else {
				out[key.Value] = v
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported YAML node")
	}
}

// Both reviewed Volcano releases use yaml.v2 for scheduler arguments. Preserve
// its YAML 1.1 boolean semantics while using yaml.v3 nodes for strict structure
// and duplicate-key checks. Quoted and explicitly tagged strings stay strings.
func nativeScalar(n *yaml.Node) scalar {
	s := scalar{n.Tag, n.Value}
	if n.Tag == "!!str" && n.Style == 0 {
		switch n.Value {
		case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
			return scalar{"!!bool", "true"}
		case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
			return scalar{"!!bool", "false"}
		}
	}
	return s
}

func isType(v any, tag string) bool { s, ok := v.(scalar); return ok && s.Tag == tag }
func text(v any) string {
	if s, ok := v.(scalar); ok && s.Tag == "!!str" {
		return s.Value
	}
	return ""
}
func requireType(v any, tag, path string) error {
	if !isType(v, tag) {
		return fmt.Errorf("%s has invalid type", path)
	}
	if tag == "!!int" {
		s := v.(scalar)
		var i int
		if err := (&yaml.Node{Kind: yaml.ScalarNode, Tag: s.Tag, Value: s.Value}).Decode(&i); err != nil {
			return fmt.Errorf("%s is outside native integer range", path)
		}
	}
	return nil
}

var nodeorderWeights = map[string]bool{
	"nodeaffinity.weight": true, "podaffinity.weight": true,
	"leastrequested.weight": true, "mostrequested.weight": true,
	"balancedresource.weight": true, "tainttoleration.weight": true,
	"imagelocality.weight": true, "podtopologyspread.weight": true,
}

func project(root map[string]any, version string) error {
	if actions, ok := root["actions"]; ok {
		if err := requireType(actions, "!!str", "actions"); err != nil {
			return err
		}
	}
	if metrics, ok := root["metrics"]; ok {
		m, ok := metrics.(map[string]any)
		if !ok {
			return fmt.Errorf("metrics must be a mapping")
		}
		for key, value := range m {
			if err := requireType(value, "!!str", "metrics."+key); err != nil {
				return err
			}
		}
	}
	if configurations, ok := root["configurations"]; ok {
		list, ok := configurations.([]any)
		if !ok {
			return fmt.Errorf("configurations must be a sequence")
		}
		for _, value := range list {
			c, ok := value.(map[string]any)
			if !ok || text(c["name"]) == "" {
				return fmt.Errorf("action configuration must have a string name")
			}
			if args, exists := c["arguments"]; exists {
				if _, ok := args.(map[string]any); !ok {
					return fmt.Errorf("configuration.arguments must be a mapping")
				}
			}
		}
	}
	tiersValue, exists := root["tiers"]
	if !exists {
		return nil
	}
	tiers, ok := tiersValue.([]any)
	if !ok {
		return fmt.Errorf("tiers must be a sequence")
	}
	keptTiers, dropped := []any{}, false
	for i, value := range tiers {
		tier, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("tiers[%d] must be a mapping", i)
		}
		pluginsValue, exists := tier["plugins"]
		if !exists {
			keptTiers = append(keptTiers, tier)
			continue
		}
		plugins, ok := pluginsValue.([]any)
		if !ok {
			return fmt.Errorf("tiers[%d].plugins must be a sequence", i)
		}
		keptPlugins, tierDropped := []any{}, false
		for j, value := range plugins {
			plugin, ok := value.(map[string]any)
			if !ok || text(plugin["name"]) == "" {
				return fmt.Errorf("tiers[%d].plugins[%d] must have a string name", i, j)
			}
			remove, err := projectPlugin(plugin, version)
			if err != nil {
				return err
			}
			if remove {
				tierDropped = true
			} else {
				keptPlugins = append(keptPlugins, plugin)
			}
		}
		if tierDropped && len(keptPlugins) == 0 && len(tier) == 1 {
			dropped = true
			continue
		}
		tier["plugins"] = keptPlugins
		keptTiers = append(keptTiers, tier)
	}
	if dropped && len(keptTiers) == 0 {
		delete(root, "tiers")
	} else {
		root["tiers"] = keptTiers
	}
	return nil
}

func projectPlugin(plugin map[string]any, version string) (bool, error) {
	name := text(plugin["name"])
	argsValue, hasArgs := plugin["arguments"]
	args := map[string]any{}
	if hasArgs {
		var ok bool
		args, ok = argsValue.(map[string]any)
		if !ok {
			return false, fmt.Errorf("%s.arguments must be a mapping", name)
		}
	}
	for key, value := range plugin {
		if pluginBooleanFields[key] {
			if err := requireType(value, "!!bool", name+"."+key); err != nil {
				return false, err
			}
		}
	}
	if name == "deviceshare" {
		for _, key := range []string{"deviceshare.GPUSharingEnable", "deviceshare.GPUNumberEnable", "deviceshare.VGPUEnable", "deviceshare.NodeLockEnable", "deviceshare.AscendMindClusterVNPUEnable", "deviceshare.AscendHAMiVNPUEnable"} {
			if version == "v1.12.0" && strings.Contains(key, "Ascend") {
				continue
			}
			if value, ok := args[key]; ok {
				if err := requireType(value, "!!bool", key); err != nil {
					return false, err
				}
			}
		}
		for _, key := range []string{"deviceshare.KnownGeometriesCMName", "deviceshare.KnownGeometriesCMNamespace"} {
			if value, ok := args[key]; ok {
				if err := requireType(value, "!!str", key); err != nil {
					return false, err
				}
			}
		}
	}
	scoring := name == "nodeorder" || name == "binpack" || name == "deviceshare"
	if flag, ok := plugin["enableNodeOrder"]; ok {
		if err := requireType(flag, "!!bool", name+".enableNodeOrder"); err != nil {
			return false, err
		}
		if scoring {
			delete(plugin, "enableNodeOrder")
		}
	}
	resources := map[string]bool{}
	if name == "binpack" {
		if list, ok := args["binpack.resources"]; ok {
			if err := requireType(list, "!!str", name+".binpack.resources"); err != nil {
				return false, err
			}
			for _, resource := range strings.Split(text(list), ",") {
				if resource = strings.TrimSpace(resource); resource != "" {
					resources["binpack.resources."+resource] = true
				}
			}
		}
	}
	for key, value := range args {
		tag := ""
		switch name {
		case "nodeorder":
			if nodeorderWeights[key] {
				tag = "!!int"
			}
		case "binpack":
			if key == "binpack.resources" {
				tag = "!!str"
			} else if key == "binpack.weight" || key == "binpack.cpu" || key == "binpack.memory" || resources[key] {
				tag = "!!int"
			}
		case "deviceshare":
			if key == "deviceshare.SchedulePolicy" {
				tag = "!!str"
			} else if key == "deviceshare.ScheduleWeight" {
				tag = "!!int"
			}
		}
		if tag != "" {
			if err := requireType(value, tag, name+".arguments["+key+"]"); err != nil {
				return false, err
			}
			delete(args, key)
		}
	}
	if hasArgs && len(args) == 0 {
		delete(plugin, "arguments")
	}
	return (name == "nodeorder" || name == "binpack") && len(plugin) == 1, nil
}

var pluginBooleanFields = map[string]bool{
	"enableJobOrder": true, "enableHierarchy": true, "enableJobReady": true,
	"enableJobPipelined": true, "enableTaskOrder": true, "enablePreemptable": true,
	"enableReclaimable": true, "enablePreemptive": true, "enableQueueOrder": true,
	"EnabledClusterOrder": true, "enablePredicate": true, "enableBestNode": true,
	"enableNodeOrder": true, "enableTargetJob": true, "enableReservedNodes": true,
	"enableJobEnqueued": true, "enabledVictim": true, "enableJobStarving": true,
	"enabledOverused": true, "enabledAllocatable": true, "enabledHyperNodeOrder": true,
	"enabledSubJobReady": true, "enabledSubJobPipelined": true,
	"enabledSubJobOrder": true, "enabledHyperNodeGradient": true,
}

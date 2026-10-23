package provider

import (
	"context"
	"net/url"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// NodePluginConfigMap selects only a managed plugin configuration. Node
// telemetry and collection remain exclusively owned by the authenticated Agent.
type NodePluginConfigMap struct {
	NodeUID   string `json:"node_uid"`
	NodeName  string `json:"node_name"`
	ConfigMap string `json:"config_map"`
}

func validateNodePluginConfigMaps(references []NodePluginConfigMap) error {
	uids, names := map[string]bool{}, map[string]bool{}
	for _, reference := range references {
		if !biz.UUID(reference.NodeUID) || !biz.Name(reference.NodeName) || !biz.Name(reference.ConfigMap) || uids[reference.NodeUID] || names[reference.NodeName] {
			return biz.Invalid("PROVIDER_CONFIG_INVALID")
		}
		uids[reference.NodeUID], names[reference.NodeName] = true, true
	}
	return nil
}

func (k *kubeReader) validatePluginConfigNodes(nodes []node) error {
	for _, reference := range k.config.NodePluginConfigMaps {
		matches := 0
		for _, actual := range nodes {
			if actual.Metadata.UID == reference.NodeUID && actual.Metadata.Name == reference.NodeName && actual.Metadata.DeletionTimestamp == "" {
				matches++
			}
		}
		if matches != 1 {
			return biz.Precondition("NODE_UNKNOWN")
		}
	}
	return nil
}

func (c Connection) pluginConfigMap(uid, name string) (string, bool) {
	for _, reference := range c.NodePluginConfigMaps {
		if reference.NodeUID == uid && reference.NodeName == name {
			return reference.ConfigMap, true
		}
	}
	return c.PluginConfigMap, false
}

func (k *kubeReader) pluginConfigDigest(ctx context.Context, name string, overridden bool) (string, error) {
	if !overridden {
		return k.configDigest(ctx, k.config.PluginNamespace, name)
	}
	var cm struct {
		Data       map[string]string `json:"data"`
		BinaryData map[string]string `json:"binaryData"`
	}
	if err := k.get(ctx, "/api/v1/namespaces/"+url.PathEscape(k.config.PluginNamespace)+"/configmaps/"+url.PathEscape(name), &cm); err != nil {
		return "", err
	}
	if cm.Data["config.json"] == "" && cm.BinaryData["config.json"] == "" {
		return "", biz.Precondition("CONFIG_DRIFT")
	}
	return biz.Hash(cm)
}

// An override is evidence only when the selected running plugin container
// actually receives that ConfigMap's config.json at the upstream fixed path.
func pluginConfigMounted(plugin pod, name string, configured container) bool {
	mounts := 0
	matched := false
	for _, mount := range configured.VolumeMounts {
		if mount.MountPath != "/config" && mount.MountPath != "/config/config.json" {
			continue
		}
		mounts++
		if mount.SubPathExpr != "" || (mount.MountPath == "/config" && mount.SubPath != "") || (mount.MountPath == "/config/config.json" && mount.SubPath != "config.json") {
			continue
		}
		volumes := 0
		for _, volume := range plugin.Spec.Volumes {
			if volume.Name != mount.Name {
				continue
			}
			volumes++
			if volume.ConfigMap == nil || volume.ConfigMap.Name != name {
				continue
			}
			if len(volume.ConfigMap.Items) == 0 {
				matched = true
				continue
			}
			for _, item := range volume.ConfigMap.Items {
				if item.Key == "config.json" && item.Path == "config.json" {
					matched = true
				}
			}
		}
		if volumes != 1 {
			return false
		}
	}
	return mounts == 1 && matched
}

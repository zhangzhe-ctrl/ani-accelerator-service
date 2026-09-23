package conf

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Validate covers transport shape for transport fixtures. ValidateRuntime adds
// the mandatory business dependencies; the production composition uses both.
func (c *Bootstrap) ValidateRuntime() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Database == nil || !filepath.IsAbs(c.Database.DsnFile) || c.Security == nil || c.Accelerator == nil {
		return fmt.Errorf("database, security and accelerator config are required")
	}
	s := c.Security
	for _, path := range []string{s.CaFile, s.CertFile, s.KeyFile, s.CursorKeyFile, c.Accelerator.ProviderConfigFile, c.Accelerator.AttestationsDir, c.Accelerator.AttestationPublicKeyFile, c.Accelerator.OwnerPublicKeyFile} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("runtime secret/config references must be absolute paths")
		}
	}
	for _, identity := range []string{s.GovernanceUri, s.InferenceUri} {
		u, err := url.Parse(identity)
		if err != nil || u.Scheme != "spiffe" || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("exact SPIFFE service URI is required")
		}
	}
	if s.GovernanceUri == s.InferenceUri {
		return fmt.Errorf("service identities must be distinct")
	}
	if c.Accelerator.MaxReplicas == 0 || c.Accelerator.MaxReplicas > 16 {
		return fmt.Errorf("max_replicas must be 1..16")
	}
	if err := validateDuration("freshness", c.Accelerator.Freshness, 5*time.Minute); err != nil {
		return err
	}
	if err := validateDuration("collect interval", c.Accelerator.CollectInterval, time.Minute); err != nil {
		return err
	}
	if c.Accelerator.CollectInterval.AsDuration() >= c.Accelerator.Freshness.AsDuration() {
		return fmt.Errorf("collect interval must be shorter than freshness")
	}
	for _, g := range s.Grants {
		if g.ActorType == "" || g.ActorId == "" || len(g.Actions) == 0 || len(g.ClusterIds) == 0 {
			return fmt.Errorf("delegation grants require actor, actions and cluster scopes")
		}
		if g.Platform && len(g.TenantIds) > 0 || !g.Platform && len(g.TenantIds) == 0 {
			return fmt.Errorf("platform and tenant grants require separate scopes")
		}
		for _, cluster := range g.ClusterIds {
			if cluster == "*" && !g.Platform {
				return fmt.Errorf("tenant cluster wildcard is forbidden")
			}
		}
		for _, action := range g.Actions {
			if action == "*" || strings.ContainsAny(action, "/ \t\r\n") {
				return fmt.Errorf("delegation actions must be exact RPC names")
			}
		}
	}
	return nil
}

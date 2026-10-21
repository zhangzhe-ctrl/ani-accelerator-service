package conf

import (
	"fmt"
	"path/filepath"
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
	if c.Accelerator.ResourceOwnerPublicKeyFile != "" && !filepath.IsAbs(c.Accelerator.ResourceOwnerPublicKeyFile) {
		return fmt.Errorf("Resource owner public key must use an absolute path")
	}
	for _, path := range []string{s.CaFile, s.CertFile, s.KeyFile, s.CursorKeyFile, c.Accelerator.ProviderConfigFile, c.Accelerator.OwnerPublicKeyFile} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("runtime secret/config references must be absolute paths")
		}
	}
	if err := s.ValidateServiceIdentities(); err != nil {
		return err
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
	return nil
}

func (s *Security) ValidateServiceIdentities() error {
	if s == nil || s.GovernanceDnsName != "ani-governance" || s.InferenceDnsName != "ani-inference-service" || s.ResourceDnsName != "ani-resource-service" {
		return fmt.Errorf("exact registered service DNS identities are required")
	}
	if s.NodeAgentDnsName != "" && s.NodeAgentDnsName != "ani-node-agent" {
		return fmt.Errorf("exact registered node agent DNS identity is required")
	}
	return nil
}

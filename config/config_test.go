package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataConfigDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.MetadataUpstreamPolicy != MetadataUpstreamPolicyAny {
		t.Fatalf("MetadataUpstreamPolicy = %q, want %q", cfg.MetadataUpstreamPolicy, MetadataUpstreamPolicyAny)
	}
	if !cfg.MetadataClientSync {
		t.Fatalf("MetadataClientSync = false, want true")
	}
	if cfg.MetadataRootCompat {
		t.Fatalf("MetadataRootCompat = true, want false")
	}
	if cfg.BouncerNetworkBind {
		t.Fatalf("BouncerNetworkBind = true, want false")
	}
}

func TestLoadMetadataConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soju.conf")
	if err := os.WriteFile(path, []byte("metadata-upstream-policy last-active\nmetadata-client-sync yes\nmetadata-root-compat yes\nbouncer-network-bind yes\n"), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.MetadataUpstreamPolicy != MetadataUpstreamPolicyLastActive {
		t.Fatalf("MetadataUpstreamPolicy = %q, want %q", cfg.MetadataUpstreamPolicy, MetadataUpstreamPolicyLastActive)
	}
	if !cfg.MetadataClientSync {
		t.Fatalf("MetadataClientSync = false, want true")
	}
	if !cfg.MetadataRootCompat {
		t.Fatalf("MetadataRootCompat = false, want true")
	}
	if !cfg.BouncerNetworkBind {
		t.Fatalf("BouncerNetworkBind = false, want true")
	}
}

func TestLoadInvalidMetadataUpstreamPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soju.conf")
	if err := os.WriteFile(path, []byte("metadata-upstream-policy sometimes\n"), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("Load returned nil error for invalid metadata-upstream-policy")
	}
}

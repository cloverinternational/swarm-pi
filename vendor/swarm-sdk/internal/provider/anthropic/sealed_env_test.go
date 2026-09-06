package anthropic

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestNewFromRegistrySealedIgnoresAmbientDebugAndTrace(t *testing.T) {
	dumpPath := filepath.Join(t.TempDir(), "sealed-raw-dump.log")
	t.Setenv("SAC_RAW_DUMP", dumpPath)
	t.Setenv("ENVELOPE_TRACE", "decoy-enabled")
	t.Setenv("CACHE_DEBUG", "decoy-enabled")

	got, err := NewFromRegistry(provider.Config{
		APIKey:       "x",
		NoAmbientEnv: true,
	})
	if err != nil {
		t.Fatalf("NewFromRegistry(sealed): %v", err)
	}
	p, ok := got.(*Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *anthropic.Provider", got)
	}
	if p.config.RawDebugWriter != nil {
		t.Fatalf("sealed RawDebugWriter = %T, want nil", p.config.RawDebugWriter)
	}
	if !p.noAmbientEnv {
		t.Fatal("sealed provider did not retain NoAmbientEnv posture")
	}
	if p.shouldWriteTrace() {
		t.Fatal("sealed provider enabled envelope tracing from ambient decoys")
	}
	if _, err := os.Stat(dumpPath); !os.IsNotExist(err) {
		t.Fatalf("sealed construction created ambient dump path %q: stat err=%v", dumpPath, err)
	}
}

func TestNewFromRegistryUnsealedStillHonorsAmbientRawDump(t *testing.T) {
	dumpPath := filepath.Join(t.TempDir(), "control-raw-dump.log")
	t.Setenv("SAC_RAW_DUMP", dumpPath)

	got, err := NewFromRegistry(provider.Config{
		APIKey:       "x",
		NoAmbientEnv: false,
	})
	if err != nil {
		t.Fatalf("NewFromRegistry(unsealed control): %v", err)
	}
	p, ok := got.(*Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *anthropic.Provider", got)
	}
	if p.config.RawDebugWriter == nil {
		t.Fatal("unsealed RawDebugWriter = nil, want ambient SAC_RAW_DUMP writer")
	}
	if p.noAmbientEnv {
		t.Fatal("unsealed control unexpectedly retained sealed posture")
	}
	if f, ok := p.config.RawDebugWriter.(*os.File); ok {
		t.Cleanup(func() { _ = f.Close() })
	}
	if _, err := os.Stat(dumpPath); err != nil {
		t.Fatalf("unsealed construction did not create ambient dump path %q: %v", dumpPath, err)
	}
}

func TestNewFromRegistrySealedRejectsOAuthBeforeAmbientRuntime(t *testing.T) {
	t.Setenv(ManagedModeEnvVar, "decoy-enabled")

	got, err := NewFromRegistry(provider.Config{
		APIKey:       "sk-ant-oat-decoy",
		NoAmbientEnv: true,
	})
	if err == nil {
		t.Fatalf("sealed OAuth construction returned provider %T, want explicit rejection", got)
	}
}

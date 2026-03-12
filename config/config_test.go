package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mickamy/sql-tap/config"
)

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()

	if cfg.GRPC != ":9091" {
		t.Errorf("GRPC = %q, want %q", cfg.GRPC, ":9091")
	}
	if cfg.DSNEnv != "DATABASE_URL" {
		t.Errorf("DSNEnv = %q, want %q", cfg.DSNEnv, "DATABASE_URL")
	}
	if cfg.SlowThreshold != 100*time.Millisecond {
		t.Errorf("SlowThreshold = %s, want 100ms", cfg.SlowThreshold)
	}
	if cfg.NPlus1.Threshold != 5 {
		t.Errorf("NPlus1.Threshold = %d, want 5", cfg.NPlus1.Threshold)
	}
	if cfg.NPlus1.Window != time.Second {
		t.Errorf("NPlus1.Window = %s, want 1s", cfg.NPlus1.Window)
	}
	if cfg.NPlus1.Cooldown != 10*time.Second {
		t.Errorf("NPlus1.Cooldown = %s, want 10s", cfg.NPlus1.Cooldown)
	}
}

func TestLoad_ExplicitPath(t *testing.T) {
	t.Parallel()

	content := `
driver: postgres
listen: ":5433"
upstream: "localhost:5432"
grpc: ":9999"
http: ":8080"
dsn_env: MY_DSN
slow_threshold: 200ms
nplus1:
  threshold: 10
  window: 2s
  cooldown: 30s
`
	path := writeTemp(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Driver != "postgres" {
		t.Errorf("Driver = %q, want %q", cfg.Driver, "postgres")
	}
	if cfg.Listen != ":5433" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":5433")
	}
	if cfg.Upstream != "localhost:5432" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "localhost:5432")
	}
	if cfg.GRPC != ":9999" {
		t.Errorf("GRPC = %q, want %q", cfg.GRPC, ":9999")
	}
	if cfg.HTTP != ":8080" {
		t.Errorf("HTTP = %q, want %q", cfg.HTTP, ":8080")
	}
	if cfg.DSNEnv != "MY_DSN" {
		t.Errorf("DSNEnv = %q, want %q", cfg.DSNEnv, "MY_DSN")
	}
	if cfg.SlowThreshold != 200*time.Millisecond {
		t.Errorf("SlowThreshold = %s, want 200ms", cfg.SlowThreshold)
	}
	if cfg.NPlus1.Threshold != 10 {
		t.Errorf("NPlus1.Threshold = %d, want 10", cfg.NPlus1.Threshold)
	}
	if cfg.NPlus1.Window != 2*time.Second {
		t.Errorf("NPlus1.Window = %s, want 2s", cfg.NPlus1.Window)
	}
	if cfg.NPlus1.Cooldown != 30*time.Second {
		t.Errorf("NPlus1.Cooldown = %s, want 30s", cfg.NPlus1.Cooldown)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	t.Parallel()

	content := `
driver: mysql
listen: ":3307"
upstream: "localhost:3306"
`
	path := writeTemp(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Driver != "mysql" {
		t.Errorf("Driver = %q, want %q", cfg.Driver, "mysql")
	}
	// Defaults should be preserved for unset fields.
	if cfg.GRPC != ":9091" {
		t.Errorf("GRPC = %q, want default %q", cfg.GRPC, ":9091")
	}
	if cfg.NPlus1.Threshold != 5 {
		t.Errorf("NPlus1.Threshold = %d, want default 5", cfg.NPlus1.Threshold)
	}
}

func TestLoad_NoDefaultFile(t *testing.T) { //nolint:paralleltest // t.Chdir is incompatible with t.Parallel
	t.Chdir(t.TempDir())

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Should return defaults.
	want := config.Default()
	if cfg.GRPC != want.GRPC {
		t.Errorf("GRPC = %q, want %q", cfg.GRPC, want.GRPC)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := config.Load("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing explicit path")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "driver: [invalid yaml")

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_UnknownField(t *testing.T) {
	t.Parallel()

	content := `
driver: postgres
grcp: ":9999"
`
	path := writeTemp(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field 'grcp'")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

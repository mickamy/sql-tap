package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the sql-tapd configuration.
type Config struct {
	Driver        string        `yaml:"driver"`
	Listen        string        `yaml:"listen"`
	Upstream      string        `yaml:"upstream"`
	GRPC          string        `yaml:"grpc"`
	HTTP          string        `yaml:"http"`
	DSNEnv        string        `yaml:"dsn_env"`
	SlowThreshold time.Duration `yaml:"slow_threshold"`
	NPlus1        NPlus1Config  `yaml:"nplus1"`
}

// NPlus1Config holds N+1 detection settings.
type NPlus1Config struct {
	Threshold int           `yaml:"threshold"`
	Window    time.Duration `yaml:"window"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// Default returns a Config with default values.
func Default() Config {
	return Config{
		GRPC:          ":9091",
		DSNEnv:        "DATABASE_URL",
		SlowThreshold: 100 * time.Millisecond,
		NPlus1: NPlus1Config{
			Threshold: 5,
			Window:    time.Second,
			Cooldown:  10 * time.Second,
		},
	}
}

// defaultConfigFile is the config file name looked up in the current directory.
const defaultConfigFile = ".sql-tap.yaml"

// Load reads the config file specified by path. If path is empty, it looks for
// the default config file in the current directory. If the default file does
// not exist, it returns the default config without error.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		path = defaultConfigFile
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is from user-provided flag or a fixed default
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}

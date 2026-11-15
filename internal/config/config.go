// Package config loads the service configuration from a YAML file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk configuration. Every field has a working default, so an empty file is a
// valid configuration.
type Config struct {
	APIBase         string   `yaml:"api_base"`
	SiteBase        string   `yaml:"site_base"`
	SiteID          int      `yaml:"site_id"`
	RequestInterval Duration `yaml:"request_interval"`
	OutputDir       string   `yaml:"output_dir"`
	BookDock        string   `yaml:"book_dock"`
	UserAgent       string   `yaml:"user_agent"`
	IncludeImages   *bool    `yaml:"include_images"`
	Auth            Auth     `yaml:"auth"`
}

// Auth carries the RanobeLib session. A token is the access token from the site's localStorage;
// a cookies file is the Netscape export from a browser extension. Either one is enough.
type Auth struct {
	Token       string `yaml:"token"`
	CookiesFile string `yaml:"cookies_file"`
}

// Duration is a time.Duration that YAML writes as "600ms" instead of nanoseconds.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		APIBase:         "https://api.cdnlibs.org/api",
		SiteBase:        "https://ranobelib.me",
		SiteID:          3,
		RequestInterval: Duration(600 * time.Millisecond),
		OutputDir:       "out",
	}
}

// Load reads a configuration file. An empty path means "look in the usual places"; a missing file
// is not an error and yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		for _, candidate := range defaultPaths() {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.APIBase == "" {
		cfg.APIBase = Default().APIBase
	}
	if cfg.SiteBase == "" {
		cfg.SiteBase = Default().SiteBase
	}
	if cfg.SiteID == 0 {
		cfg.SiteID = Default().SiteID
	}
	if cfg.RequestInterval == 0 {
		cfg.RequestInterval = Default().RequestInterval
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = Default().OutputDir
	}
	return cfg, nil
}

func defaultPaths() []string {
	paths := []string{"yomibako.yml", "yomibako.yaml"}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "yomibako", "config.yml"))
	}
	return paths
}

// ImagesEnabled reports whether chapter illustrations should be embedded.
func (c Config) ImagesEnabled() bool {
	return c.IncludeImages == nil || *c.IncludeImages
}

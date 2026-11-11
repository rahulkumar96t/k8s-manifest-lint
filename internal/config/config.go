// Package config loads the optional .k8s-lint.yaml file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"go.yaml.in/yaml/v3"
)

// DefaultFile is looked up in the working directory when --config is not set.
const DefaultFile = ".k8s-lint.yaml"

// Config is the on-disk configuration. Zero values mean "use the default";
// an explicitly empty list (requiredLabels: []) means "require nothing".
type Config struct {
	// FailOn is the lowest severity that makes the run fail
	// (error, warning, info or none). The --fail-on flag overrides it.
	FailOn string `yaml:"failOn"`
	// AllowedRegistries lists registry hosts or host/path prefixes images
	// may come from. Segments may use path.Match globs, e.g.
	// "*.dkr.ecr.*.amazonaws.com". Empty means the rule does nothing.
	AllowedRegistries []string `yaml:"allowedRegistries"`
	// RequiredLabels must be present in every workload's metadata.labels.
	RequiredLabels []string `yaml:"requiredLabels"`
	// Resources lists the resource names that must have requests/limits.
	Resources struct {
		Requests []string `yaml:"requests"`
		Limits   []string `yaml:"limits"`
	} `yaml:"resources"`
	// Rules overrides individual rules by id.
	Rules map[string]RuleConfig `yaml:"rules"`
}

// RuleConfig enables/disables a rule or changes its severity.
type RuleConfig struct {
	Enabled  *bool  `yaml:"enabled"`
	Severity string `yaml:"severity"`
}

// Default returns the built-in configuration.
func Default() *Config {
	c := &Config{}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	if c.RequiredLabels == nil {
		c.RequiredLabels = []string{"app.kubernetes.io/name"}
	}
	if c.Resources.Requests == nil {
		c.Resources.Requests = []string{"cpu", "memory"}
	}
	if c.Resources.Limits == nil {
		c.Resources.Limits = []string{"cpu", "memory"}
	}
	if c.Rules == nil {
		c.Rules = map[string]RuleConfig{}
	}
}

// Parse decodes a config document. Unknown keys are rejected so a typo
// like "requiredLabel" fails loudly instead of being silently ignored.
func Parse(data []byte) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	c.applyDefaults()
	return c, nil
}

// Load reads the config at path. With an empty path it tries DefaultFile in
// the working directory and falls back to the defaults if it is absent.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

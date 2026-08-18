package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Step represents a single build step from config.yaml.
type Step struct {
	Name    string            `yaml:"name"`
	Run     string            `yaml:"run"`
	Needs   []string          `yaml:"needs,omitempty"`
	Inputs  []string          `yaml:"inputs,omitempty"`
	Outputs []string          `yaml:"outputs,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Retries int               `yaml:"retries,omitempty"`
	Timeout int               `yaml:"timeout,omitempty"` // seconds
	CWD     string            `yaml:"cwd,omitempty"`
}

// BuildSpec is the in-memory representation of a parsed config.
type BuildSpec struct {
	Steps []Step `yaml:"steps"`
	Path  string
}

// Load reads and validates a YAML config file.
func Load(path string) (*BuildSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var spec BuildSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	spec.Path = path

	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Validate checks the config for schema errors.
func (s *BuildSpec) Validate() error {
	if len(s.Steps) == 0 {
		return fmt.Errorf("config %s: steps list is empty or missing", s.Path)
	}

	names := make(map[string]int, len(s.Steps))
	for i, step := range s.Steps {
		line := i + 1
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("config %s: step at index %d: missing required field 'name'", s.Path, line)
		}
		if strings.TrimSpace(step.Run) == "" {
			return fmt.Errorf("config %s: step '%s': missing required field 'run'", s.Path, step.Name)
		}
		if prev, ok := names[step.Name]; ok {
			return fmt.Errorf("config %s: duplicate step name '%s' (also defined at step index %d)", s.Path, step.Name, prev+1)
		}
		names[step.Name] = i
	}

	for _, step := range s.Steps {
		for _, dep := range step.Needs {
			if _, ok := names[dep]; !ok {
				return fmt.Errorf("config %s: step '%s': needs references undefined step '%s'", s.Path, step.Name, dep)
			}
		}
	}
	return nil
}

// StepByName returns a map of step name to step.
func (s *BuildSpec) StepByName() map[string]Step {
	m := make(map[string]Step, len(s.Steps))
	for _, step := range s.Steps {
		m[step.Name] = step
	}
	return m
}

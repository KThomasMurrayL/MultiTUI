// Package config loads agent presets and validates commands before tmux starts.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

var Layouts = []string{"tiled", "even-horizontal", "even-vertical", "main-vertical"}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
var envPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type Agent struct {
	Cmd  string            `yaml:"cmd"`
	Args []string          `yaml:"args"`
	Env  map[string]string `yaml:"env"`
}

type Defaults struct {
	Layout       string `yaml:"layout"`
	KillOnDetach bool   `yaml:"kill_on_detach"`
}

type Config struct {
	Agents   map[string]Agent `yaml:"agents"`
	Defaults Defaults         `yaml:"defaults"`
}

func Builtin() Config {
	return Config{
		Agents: map[string]Agent{
			"claude": {Cmd: "claude"}, "codex": {Cmd: "codex"}, "opencode": {Cmd: "opencode"},
		},
		Defaults: Defaults{Layout: "tiled"},
	}
}

func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute directory")
		}
		return filepath.Join(dir, "mt", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "mt", "config.yaml"), nil
}

// Load overlays user presets on the builtins. A missing default file is fine;
// an explicitly requested file must exist. Unknown YAML fields are rejected.
func Load(path string, explicit bool) (Config, error) {
	cfg := Builtin()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var overlay struct {
		Agents   map[string]Agent `yaml:"agents"`
		Defaults struct {
			Layout       *string `yaml:"layout"`
			KillOnDetach *bool   `yaml:"kill_on_detach"`
		} `yaml:"defaults"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&overlay); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config %s must contain a single YAML document", path)
	}
	for name, agent := range overlay.Agents {
		cfg.Agents[name] = agent
	}
	if overlay.Defaults.Layout != nil {
		cfg.Defaults.Layout = *overlay.Defaults.Layout
	}
	if overlay.Defaults.KillOnDetach != nil {
		cfg.Defaults.KillOnDetach = *overlay.Defaults.KillOnDetach
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

func ValidLayout(layout string) bool {
	for _, candidate := range Layouts {
		if layout == candidate {
			return true
		}
	}
	return false
}

func (c Config) Names() []string {
	names := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c Config) Validate() error {
	if !ValidLayout(c.Defaults.Layout) {
		return fmt.Errorf("invalid default layout %q (choose %s)", c.Defaults.Layout, strings.Join(Layouts, ", "))
	}
	for _, name := range c.Names() {
		if !ValidName(name) || name == "ls" || name == "attach" || name == "kill" || name == "help" || name == "all" {
			return fmt.Errorf("invalid or reserved agent name %q", name)
		}
		if err := c.Agents[name].Validate(); err != nil {
			return fmt.Errorf("agent %s: %w", name, err)
		}
	}
	return nil
}

func (a Agent) Validate() error {
	if strings.TrimSpace(a.Cmd) == "" {
		return errors.New("cmd must be an executable name or path; put arguments in args")
	}
	values := append([]string{a.Cmd}, a.Args...)
	for key, value := range a.Env {
		if !envPattern.MatchString(key) {
			return fmt.Errorf("invalid environment variable %q", key)
		}
		values = append(values, value)
	}
	for _, value := range values {
		if strings.ContainsFunc(value, unicode.IsControl) {
			return errors.New("cmd, args and env must not contain control characters")
		}
	}
	return nil
}

// Command resolves the executable using the preset's PATH and working directory,
// then quotes every argument literally for the pane's shell.
func (a Agent) Command(cwd string) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	binary, err := a.resolve(cwd)
	if err != nil {
		return "", fmt.Errorf("agent executable %q not found or not executable; install it or update config: %w", a.Cmd, err)
	}
	parts := []string{"/usr/bin/env"}
	keys := make([]string, 0, len(a.Env))
	for key := range a.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, Quote(key+"="+a.Env[key]))
	}
	parts = append(parts, Quote(binary))
	for _, arg := range a.Args {
		parts = append(parts, Quote(arg))
	}
	return strings.Join(parts, " "), nil
}

func (a Agent) resolve(cwd string) (string, error) {
	if strings.ContainsRune(a.Cmd, filepath.Separator) {
		path := a.Cmd
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if executable(path) {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
	path, custom := a.Env["PATH"]
	if !custom {
		path = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, a.Cmd)
		if executable(candidate) {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

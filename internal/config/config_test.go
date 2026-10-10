package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPresetsAndDefaults(t *testing.T) {
	path := writeConfig(t, "agents:\n  custom:\n    cmd: my-agent\n    args: [\"--model\", \"test model\"]\n    env: {MODE: dev}\n  claude: {cmd: claude-dev}\ndefaults:\n  layout: main-vertical\n  kill_on_detach: true\n")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["claude"].Cmd != "claude-dev" || cfg.Agents["codex"].Cmd != "codex" {
		t.Fatalf("presets not merged: %+v", cfg.Agents)
	}
	if cfg.Defaults.Layout != "main-vertical" || !cfg.Defaults.KillOnDetach {
		t.Fatalf("defaults not loaded: %+v", cfg.Defaults)
	}
	if !reflect.DeepEqual(cfg.Names(), []string{"claude", "codex", "custom", "opencode"}) {
		t.Fatal(cfg.Names())
	}
}

func TestMissingAndEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := Load(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil {
		t.Fatal("explicit missing config should fail")
	}
	for _, text := range []string{"", "# comment\n", "defaults: {}\n"} {
		cfg, err := Load(writeConfig(t, text), true)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Defaults.Layout != "tiled" {
			t.Fatal(cfg.Defaults)
		}
	}
}

func TestRejectInvalidConfig(t *testing.T) {
	for _, text := range []string{
		"defaults: {layout: diagonal}",
		"defaults: {kill_on_detatch: true}",
		"agents: {custom: {cmd: foo, argz: []}}",
		"agents: {custom: {args: [foo]}}",
		"agents: {ls: {cmd: foo}}",
		"agents: {bad.name: {cmd: foo}}",
		"agents: {custom: {cmd: foo, env: {BAD-NAME: value}}}",
		"agents: {custom: {cmd: foo, args: [\"foo\\nbar\"]}}",
		"agents: {custom: {cmd: foo, env: {VALUE: \"bad\\u001b\"}}}",
		"agents: {custom: {cmd: foo}}\n---\ndefaults: {layout: tiled}",
		"agents: [not-a-map]",
		"defaults: {layout: tiled, layout: main-vertical}",
	} {
		t.Run(text, func(t *testing.T) {
			if _, err := Load(writeConfig(t, text), true); err == nil {
				t.Fatalf("accepted invalid config %s", text)
			}
		})
	}
}

func TestCommandLiteralArgumentsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agent with spaces")
	script := "#!/bin/sh\nprintf '%s\\n' \"$TEST_VALUE\"\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	a := Agent{Cmd: "./agent with spaces", Args: []string{"quote's", "$(touch should-not-exist)", "; echo unsafe", "", "Enter"}, Env: map[string]string{"TEST_VALUE": "value's $literal"}}
	command, err := a.Command(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	want := "value's $literal\nquote's\n$(touch should-not-exist)\n; echo unsafe\n\nEnter\n"
	if string(output) != want {
		t.Fatalf("got %q, want %q (command %s)", output, want, command)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("argument was interpreted as shell code")
	}
}

func TestPresetPATHAndMissingExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := Agent{Cmd: "custom-agent", Env: map[string]string{"PATH": dir}}
	command, err := a.Command(dir)
	if err != nil || !strings.Contains(command, Quote(path)) {
		t.Fatalf("%q: %v", command, err)
	}
	if _, err := (Agent{Cmd: "missing-agent", Env: map[string]string{"PATH": dir}}).Command(dir); err == nil {
		t.Fatal("missing executable accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Command(dir); err == nil {
		t.Fatal("non-executable file accepted")
	}
}

func TestConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := Path()
	if err != nil || path != filepath.Join(dir, "mt", "config.yaml") {
		t.Fatalf("%s: %v", path, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := Path(); err == nil {
		t.Fatal("relative XDG_CONFIG_HOME accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", dir)
	path, err = Path()
	if err != nil || path != filepath.Join(dir, ".config", "mt", "config.yaml") {
		t.Fatalf("%s: %v", path, err)
	}
}

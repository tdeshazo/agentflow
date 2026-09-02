package agentflowcli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const configFileName = "config.toml"

type selectorDefaults struct {
	Workflow *string `toml:"workflow"`
}

type runDefaults struct {
	Workflow *string `toml:"workflow"`
	Detach   *bool   `toml:"detach"`
}

type statusDefaults struct {
	Workflow *string `toml:"workflow"`
	JSON     *bool   `toml:"json"`
	All      *bool   `toml:"all"`
	Detail   *bool   `toml:"detail"`
}

type planDefaults struct {
	Workflow *string `toml:"workflow"`
	Expanded *bool   `toml:"expanded"`
}

type logsDefaults struct {
	Workflow *string `toml:"workflow"`
	Tail     *int    `toml:"tail"`
	Follow   *bool   `toml:"follow"`
}

type cliConfig struct {
	CodexBin   *string           `toml:"codex_bin"`
	Parameters map[string]string `toml:"parameters"`
	Run        runDefaults       `toml:"run"`
	Status     statusDefaults    `toml:"status"`
	Reset      selectorDefaults  `toml:"reset"`
	Validate   selectorDefaults  `toml:"validate"`
	Plan       planDefaults      `toml:"plan"`
	Logs       logsDefaults      `toml:"logs"`
}

func loadCLIConfig(root string, homeDir func() (string, error)) (cliConfig, error) {
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	home, err := homeDir()
	if err != nil {
		return cliConfig{}, fmt.Errorf("resolve home directory for Agentflow config: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return cliConfig{}, fmt.Errorf("resolve global Agentflow config directory: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return cliConfig{}, fmt.Errorf("resolve local Agentflow config directory: %w", err)
	}

	var merged cliConfig
	for _, path := range []string{
		filepath.Join(home, ".agentflow", configFileName),
		filepath.Join(root, ".agentflow", configFileName),
	} {
		layer, found, err := readCLIConfig(path)
		if err != nil {
			return cliConfig{}, err
		}
		if found {
			overlayCLIConfig(&merged, layer)
		}
	}
	return merged, nil
}

func readCLIConfig(path string) (cliConfig, bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return cliConfig{}, false, nil
	}
	if err != nil {
		return cliConfig{}, false, fmt.Errorf("read Agentflow config %s: %w", path, err)
	}
	defer file.Close()

	var config cliConfig
	if err := toml.NewDecoder(file).DisallowUnknownFields().Decode(&config); err != nil {
		return cliConfig{}, false, fmt.Errorf("decode Agentflow config %s: %w", path, err)
	}
	if err := validateCLIConfig(config); err != nil {
		return cliConfig{}, false, fmt.Errorf("invalid Agentflow config %s: %w", path, err)
	}
	return config, true, nil
}

func validateCLIConfig(config cliConfig) error {
	if _, exists := config.Parameters[""]; exists {
		return fmt.Errorf("parameters must not contain an empty key")
	}
	selectors := []struct {
		field string
		value *string
	}{
		{"run.workflow", config.Run.Workflow},
		{"status.workflow", config.Status.Workflow},
		{"reset.workflow", config.Reset.Workflow},
		{"validate.workflow", config.Validate.Workflow},
		{"plan.workflow", config.Plan.Workflow},
		{"logs.workflow", config.Logs.Workflow},
	}
	for _, selector := range selectors {
		if selector.value != nil {
			if err := workflow.ValidateSelector(*selector.value); err != nil {
				return fmt.Errorf("%s: %w", selector.field, err)
			}
		}
	}
	if config.Status.All != nil && *config.Status.All && config.Status.Workflow != nil {
		return fmt.Errorf("status.all and status.workflow are mutually exclusive")
	}
	if config.Status.All != nil && *config.Status.All && config.Status.Detail != nil && *config.Status.Detail {
		return fmt.Errorf("status.all and status.detail are mutually exclusive")
	}
	if config.Logs.Tail != nil && *config.Logs.Tail < 0 {
		return fmt.Errorf("logs.tail must not be negative")
	}
	if config.Logs.Follow != nil && *config.Logs.Follow && config.Logs.Tail != nil {
		return fmt.Errorf("logs.tail and logs.follow are mutually exclusive")
	}
	return nil
}

func overlayCLIConfig(dst *cliConfig, src cliConfig) {
	overlay(&dst.CodexBin, src.CodexBin)
	if dst.Parameters == nil && len(src.Parameters) > 0 {
		dst.Parameters = make(map[string]string, len(src.Parameters))
	}
	for key, value := range src.Parameters {
		dst.Parameters[key] = value
	}
	overlay(&dst.Run.Workflow, src.Run.Workflow)
	overlay(&dst.Run.Detach, src.Run.Detach)
	if src.Status.Workflow != nil {
		dst.Status.Workflow = src.Status.Workflow
		dst.Status.All = nil
	}
	if src.Status.All != nil {
		dst.Status.All = src.Status.All
		if *src.Status.All {
			dst.Status.Workflow = nil
			dst.Status.Detail = nil
		}
	}
	overlay(&dst.Status.JSON, src.Status.JSON)
	if src.Status.Detail != nil {
		dst.Status.Detail = src.Status.Detail
		if *src.Status.Detail {
			dst.Status.All = nil
		}
	}
	overlaySelector(&dst.Reset, src.Reset)
	overlaySelector(&dst.Validate, src.Validate)
	overlay(&dst.Plan.Workflow, src.Plan.Workflow)
	overlay(&dst.Plan.Expanded, src.Plan.Expanded)
	overlay(&dst.Logs.Workflow, src.Logs.Workflow)
	if src.Logs.Tail != nil {
		dst.Logs.Tail = src.Logs.Tail
		dst.Logs.Follow = nil
	}
	if src.Logs.Follow != nil {
		dst.Logs.Follow = src.Logs.Follow
		if *src.Logs.Follow {
			dst.Logs.Tail = nil
		}
	}
}

func overlaySelector(dst *selectorDefaults, src selectorDefaults) {
	overlay(&dst.Workflow, src.Workflow)
}

func overlay[T any](dst **T, src *T) {
	if src != nil {
		*dst = src
	}
}

func configWorkflow(config cliConfig, command string) string {
	var value *string
	switch command {
	case "run":
		value = config.Run.Workflow
	case "status":
		value = config.Status.Workflow
	case "reset":
		value = config.Reset.Workflow
	case "validate":
		value = config.Validate.Workflow
	case "plan":
		value = config.Plan.Workflow
	case "logs":
		value = config.Logs.Workflow
	}
	if value == nil {
		return ""
	}
	return *value
}

func configuredString(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

func configuredBool(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}
	return fallback
}

func configuredInt(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

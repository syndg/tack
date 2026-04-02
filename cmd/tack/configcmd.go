package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/config"
	"gopkg.in/yaml.v3"
)

var configUser bool
var configProject bool

func init() {
	configCmd.PersistentFlags().BoolVar(&configUser, "user", false, "target user config (~/.config/tack/config.yaml)")
	configCmd.PersistentFlags().BoolVar(&configProject, "project", false, "target project config (.tack/config.yaml)")
	configGetCmd.Flags().BoolVar(&configUser, "user", false, "show user-layer value only")
	configGetCmd.Flags().BoolVar(&configProject, "project", false, "show project-layer value only")
	configListCmd.Flags().BoolVar(&configUser, "user", false, "show user config only")
	configListCmd.Flags().BoolVar(&configProject, "project", false, "show project config only")

	configCmd.AddCommand(configSetCmd, configGetCmd, configListCmd, configRemoveCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Tack configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := targetConfigPath()
		if err != nil {
			return err
		}
		if path == "" {
			return fmt.Errorf("no config path resolved (use --user or run from a project with .tack/)")
		}

		data, err := readYAMLMap(path)
		if err != nil {
			return err
		}

		setNestedValue(data, args[0], args[1])

		return writeYAMLMap(path, data)
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a config value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg *config.Config
		var err error

		switch {
		case configUser:
			cfg, err = config.Load("", userConfigPath())
		case configProject:
			var path string
			path, err = projectConfigPath()
			if err == nil {
				cfg, err = config.Load(path, "")
			}
		default:
			cfg, err = loadConfig()
		}
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Marshal config to map for key lookup
		raw, _ := yaml.Marshal(cfg)
		var m map[string]interface{}
		yaml.Unmarshal(raw, &m)

		val := getNestedValue(m, args[0])
		if val == nil {
			return fmt.Errorf("key %q not found", args[0])
		}

		switch v := val.(type) {
		case map[string]interface{}, []interface{}:
			out, _ := yaml.Marshal(v)
			fmt.Print(string(out))
		default:
			fmt.Println(v)
		}
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List config values",
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg *config.Config
		var err error

		switch {
		case configUser:
			cfg, err = config.Load("", userConfigPath())
		case configProject:
			var path string
			path, err = projectConfigPath()
			if err == nil {
				cfg, err = config.Load(path, "")
			}
		default:
			cfg, err = loadConfig()
		}
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		out, _ := yaml.Marshal(cfg)
		fmt.Print(string(out))
		return nil
	},
}

var configRemoveCmd = &cobra.Command{
	Use:   "remove <key>",
	Short: "Remove a config key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := targetConfigPath()
		if err != nil {
			return err
		}
		if path == "" {
			return fmt.Errorf("no config path resolved (use --user or run from a project with .tack/)")
		}

		data, err := readYAMLMap(path)
		if err != nil {
			return err
		}

		removeNestedValue(data, args[0])

		return writeYAMLMap(path, data)
	},
}

// targetConfigPath returns the path for write operations (set/remove).
// Default is project config; --user targets user config.
func targetConfigPath() (string, error) {
	if configUser {
		return userConfigPath(), nil
	}
	return projectConfigPath()
}

func userConfigPath() string {
	if v := os.Getenv("TACK_USER_CONFIG_PATH"); v != "" {
		return v
	}
	return config.UserConfigPath
}

func projectConfigPath() (string, error) {
	return config.ResolveProjectConfig(cfgPath)
}

func readYAMLMap(path string) (map[string]interface{}, error) {
	expanded := os.ExpandEnv(path)
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		if home != "" {
			expanded = home + path[1:]
		}
	}

	raw, err := os.ReadFile(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var m map[string]interface{}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m == nil {
		m = make(map[string]interface{})
	}
	return m, nil
}

func writeYAMLMap(path string, data map[string]interface{}) error {
	expanded := path
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		if home != "" {
			expanded = home + path[1:]
		}
	}

	dir := dirOf(expanded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(expanded, out, 0o644)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func setNestedValue(m map[string]interface{}, dottedKey, value string) {
	parts := strings.Split(dottedKey, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part]
		if !ok {
			next = make(map[string]interface{})
			current[part] = next
		}
		if nextMap, ok := next.(map[string]interface{}); ok {
			current = nextMap
		} else {
			// Overwrite non-map with a new map
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		}
	}
}

func getNestedValue(m map[string]interface{}, dottedKey string) interface{} {
	parts := strings.Split(dottedKey, ".")
	var current interface{} = m
	for _, part := range parts {
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current, ok = currentMap[part]
		if !ok {
			return nil
		}
	}
	return current
}

func removeNestedValue(m map[string]interface{}, dottedKey string) {
	parts := strings.Split(dottedKey, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			delete(current, part)
			return
		}
		next, ok := current[part]
		if !ok {
			return
		}
		nextMap, ok := next.(map[string]interface{})
		if !ok {
			return
		}
		current = nextMap
	}
}

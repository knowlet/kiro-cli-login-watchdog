package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile loads KEY=VALUE entries without overwriting variables already
// present in the process environment. It intentionally implements only the
// dotenv subset useful for service configuration.
func LoadEnvFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open env file: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("env file line %d: expected KEY=VALUE", lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return fmt.Errorf("env file line %d: empty key", lineNo)
		}
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("set %s: %w", key, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read env file: %w", err)
	}
	return nil
}

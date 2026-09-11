package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv reads simple KEY=VALUE entries from path. Missing files are empty configurations.
func LoadDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("read dotenv: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') {
			quote := value[0]
			if value[len(value)-1] != quote {
				return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
			}
			value = value[1 : len(value)-1]
		} else if len(value) == 1 && (value[0] == '\'' || value[0] == '"') {
			return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dotenv at line %d: %w", lineNumber+1, err)
	}
	return values, nil
}

// MergeLookup prefers a non-empty process value over the dotenv value.
func MergeLookup(dotenv map[string]string, getenv func(string) string, key string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return dotenv[key]
}

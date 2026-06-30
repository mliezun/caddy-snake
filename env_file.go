package caddysnake

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var validEnvVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateEnvVarName(name string) error {
	if !validEnvVarName.MatchString(name) {
		return fmt.Errorf("must match [A-Za-z_][A-Za-z0-9_]*")
	}
	if name == "PYTHONUNBUFFERED" || strings.HasPrefix(name, "CADDYSNAKE_") {
		return fmt.Errorf("reserved environment variable name")
	}
	return nil
}

func validateEnvVars(envVars map[string]string) error {
	for name := range envVars {
		if err := validateEnvVarName(name); err != nil {
			return fmt.Errorf("invalid env_var name %q: %w", name, err)
		}
	}
	return nil
}

func envMapContainsPlaceholder(envVars map[string]string) bool {
	for _, v := range envVars {
		if containsPlaceholder(v) {
			return true
		}
	}
	return false
}

func envFilesContainPlaceholder(paths []string) bool {
	for _, p := range paths {
		if containsPlaceholder(p) {
			return true
		}
	}
	return false
}

func cloneEnvVars(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneEnvFiles(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func resolveEnvFilePath(workingDir, envFile string) (string, error) {
	if envFile == "" {
		return "", fmt.Errorf("env_file path is empty")
	}
	if hasDotDotSegment(envFile) {
		return "", fmt.Errorf("env_file path contains path traversal: %q", envFile)
	}
	path := envFile
	if !filepath.IsAbs(path) {
		base := workingDir
		if base == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolve env_file base directory: %w", err)
			}
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid env_file path: %w", err)
	}
	return abs, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: invalid line (expected KEY=VALUE)", path, lineNum)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty variable name", path, lineNum)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		vars[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return vars, nil
}

func loadEnvFiles(workingDir string, paths []string) (map[string]string, error) {
	merged := make(map[string]string)
	for _, p := range paths {
		if p == "" {
			continue
		}
		if containsPlaceholder(p) {
			return nil, fmt.Errorf("env_file path contains unresolved placeholder: %q", p)
		}
		abs, err := resolveEnvFilePath(workingDir, p)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("env_file %q: %w", abs, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("env_file %q is a directory", abs)
		}
		vars, err := parseEnvFile(abs)
		if err != nil {
			return nil, fmt.Errorf("env_file %q: %w", abs, err)
		}
		for k, v := range vars {
			merged[k] = v
		}
	}
	return merged, nil
}

func parseEnvSlice(base []string) map[string]string {
	m := make(map[string]string, len(base))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if idx := strings.Index(key, "\x00"); idx >= 0 {
			key = key[:idx]
		}
		m[key] = value
	}
	return m
}

func envMapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// buildWorkerEnv merges environment variables with precedence:
// base (process env) < fileVars < inlineVars < extra (internal vars).
func buildWorkerEnv(base []string, fileVars, inlineVars map[string]string, extra ...string) []string {
	merged := parseEnvSlice(base)
	for k, v := range fileVars {
		merged[k] = v
	}
	for k, v := range inlineVars {
		merged[k] = v
	}
	for _, entry := range extra {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		merged[key] = value
	}
	return envMapToSlice(merged)
}

func workerInternalEnv(iface, cacheAddr, workerID string) []string {
	extra := []string{"PYTHONUNBUFFERED=1"}
	if cacheAddr != "" {
		extra = append(extra,
			EnvCaddysnakeCacheAddr+"="+cacheAddr,
			EnvCaddysnakeWorkerInterface+"="+iface,
			EnvCaddysnakeCacheTimeoutSeconds+"="+strconv.Itoa(DefaultCacheClientTimeoutSec),
		)
		if workerID != "" {
			extra = append(extra, EnvCaddysnakeWorkerID+"="+workerID)
		}
	}
	return extra
}

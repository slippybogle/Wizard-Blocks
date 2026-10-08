package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// WithEnvFile returns an env lookup where WB_* values from the file named by
// WB_ENV_FILE override the process environment. A missing file is not an
// error. It lets an app-store install (e.g. Umbrel) override node settings
// from its persistent data dir without editing the managed compose file.
func WithEnvFile(env func(string) string) (func(string) string, string, error) {
	path := env("WB_ENV_FILE")
	if path == "" {
		return env, "", nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return env, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k = strings.TrimSpace(k)
		if !ok || !strings.HasPrefix(k, "WB_") {
			return nil, "", fmt.Errorf("%s:%d: expected WB_NAME=value", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		vals[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}
	return func(k string) string {
		if v, ok := vals[k]; ok {
			return v
		}
		return env(k)
	}, path, nil
}

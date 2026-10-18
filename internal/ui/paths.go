package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tildePath shortens a path under the home directory to ~/....
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return p
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding home: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

// Copyright 2026 Mateusz Urbanek.

package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestInvalidCLIConfig(t *testing.T) {
	for _, args := range [][]string{{}, {"--upstream", "https://user:password@example.com/mcp"}, {"--upstream", "ftp://example.com/mcp"}} {
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "upstream") {
			t.Errorf("args %q: expected upstream config error; output %s; err %v", args, output, err)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

func TestICMPPreflightCLIHelper(t *testing.T) {
	if os.Getenv("BASTION_CLI_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"bastion-agent"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("helper arguments missing")
}

func TestICMPPreflightDoesNotReadConfigAndRejectsConflictingFamilies(t *testing.T) {
	for _, entry := range []struct {
		name string
		args []string
		json bool
	}{
		{"IPv4", []string{"-config", "nonexistent-private-config", "-probe-icmp4", "127.0.0.1"}, true},
		{"IPv6", []string{"-config", "nonexistent-private-config", "-probe-icmp6", "::1"}, true},
		{"conflict", []string{"-probe-icmp4", "127.0.0.1", "-probe-icmp6", "::1"}, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestICMPPreflightCLIHelper$", "--"}, entry.args...)
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), "BASTION_CLI_TEST_HELPER=1")
			raw, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatal("preflight must fail closed", err, string(raw))
			}
			if entry.json {
				var observation status.Observation
				if json.Unmarshal(raw, &observation) != nil || observation.Status != "unknown" || observation.LatencyMS != nil {
					t.Fatal("invalid target must emit only sanitized unknown observation", string(raw))
				}
			} else if !strings.Contains(string(raw), "choose only one ICMP preflight family") {
				t.Fatal("ambiguous family not rejected", string(raw))
			}
		})
	}
}

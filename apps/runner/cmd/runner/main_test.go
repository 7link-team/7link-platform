package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLICompatibilityAndUnavailableCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"help"}} {
		var out bytes.Buffer
		if err := run(t.Context(), args, &out); err != nil || out.Len() == 0 {
			t.Fatalf("%v: %v", args, err)
		}
		if len(args) == 0 && !strings.Contains(out.String(), "task execution and daemon are not implemented") {
			t.Fatal("default behavior changed")
		}
	}
	for _, args := range [][]string{{"login"}, {"daemon"}, {"inspect", "extra"}, {"service"}, {"service", "install"}, {"service", "unknown", "--profile", t.TempDir()}, {"service", "status", "--profile", t.TempDir(), "extra"}} {
		if err := run(t.Context(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("unsupported capability claimed: %v", args)
		}
	}
}

func TestVersionReportsBuildStamp(t *testing.T) {
	previous := version
	version = "v1.2.3-rc.1"
	t.Cleanup(func() { version = previous })
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "v1.2.3-rc.1\n" {
		t.Fatalf("version = %q", out.String())
	}
	if err := run(t.Context(), []string{"--version", "unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("version accepted unexpected arguments")
	}
}

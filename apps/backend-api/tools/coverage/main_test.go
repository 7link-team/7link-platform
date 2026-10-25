package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBlocksMergesAtomicCountsAndUnionsHits(t *testing.T) {
	profile := "mode: atomic\n" +
		"example.test/module/a.go:1.1,2.2 9 0\n" +
		"example.test/module/a.go:1.1,2.2 9 4\n" +
		"example.test/module/a.go:1.1,2.2 9 3\n" +
		"example.test/module/a.go:3.1,4.2 1 0\n"
	blocks, err := readBlocks(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks["example.test/module/a.go:1.1,2.2"] != (block{9, 7}) ||
		blocks["example.test/module/a.go:3.1,4.2"] != (block{1, 0}) {
		t.Fatalf("lost weighted blocks or atomic counts: %v", blocks)
	}
}

func TestReadBlocksFailsClosed(t *testing.T) {
	for _, profile := range []string{
		"", "mode: set\n", "mode: count\n", "mode: atomic\ntruncated\n",
		"mode: atomic\na.go:not-a-location 1 1\n",
		"mode: atomic\na.go:1.1,2.2 -1 1\n",
		"mode: atomic\na.go:1.1,2.2 1 -1\n",
		"mode: atomic\na.go:1.1,2.2 1 9223372036854775808\n",
		"mode: atomic\na.go:1.1,2.2 1 0\na.go:1.1,2.2 2 1\n",
		"mode: atomic\na.go:1.1,2.2 1 9223372036854775807\na.go:1.1,2.2 1 1\n",
	} {
		t.Run(profile, func(t *testing.T) {
			if _, err := readBlocks(strings.NewReader(profile)); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	if _, err := readBlocks(io.MultiReader(strings.NewReader("mode: atomic\n"), failingReader{})); err == nil {
		t.Fatal("read error ignored")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestRunPreservesSeparateTotalAndModuleFilters(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.out")
	output := filepath.Join(dir, "merged.out")
	filtered := filepath.Join(dir, "filtered.out")
	profile := "mode: atomic\n" +
		"example.test/module/a.go:1.1,2.2 9 0\n" +
		"example.test/module/a.go:3.1,4.2 1 0\n" +
		"example.test/module/a.go:1.1,2.2 9 7\n" +
		"example.test/module/types.gen.go:1.1,2.2 10 0\n" +
		"example.test/query/gen.go:1.1,2.2 3 1\n" +
		"example.test/cmd/7link-api/main.go:1.1,2.2 4 0\n" +
		"example.test/cmd/infra-api/main.go:1.1,2.2 4 0\n" +
		"example.test/pkg/extension/gorm/gen/gen.go:1.1,2.2 5 1\n"
	if err := os.WriteFile(input, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	err := run([]string{"-input", input, "-output", output, "-filtered", filtered,
		"-minimum", "45", "example.test/module", "example.test/query"}, &report)
	if err != nil {
		t.Fatal(err)
	}
	// Module floors include generated files, as the old unfiltered raw gate did.
	if !strings.Contains(report.String(), "45.0% (minimum 45.0%)") ||
		!strings.Contains(report.String(), "100.0% (minimum 45.0%)") {
		t.Fatalf("wrong module weights/filters: %s", &report)
	}
	got, err := os.ReadFile(filtered)
	if err != nil {
		t.Fatal(err)
	}
	want := "mode: atomic\n" +
		"example.test/module/a.go:1.1,2.2 9 7\n" +
		"example.test/module/a.go:3.1,4.2 1 0\n" +
		"example.test/pkg/extension/gorm/gen/gen.go:1.1,2.2 5 1\n"
	if string(got) != want {
		t.Fatalf("total filter changed:\ngot %s\nwant %s", got, want)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 8 || !strings.Contains(string(raw), "types.gen.go") {
		t.Fatalf("raw blocks lost or duplicated: %s", raw)
	}
	// Input order must not affect the normalized bytes or reports.
	lines := strings.Split(strings.TrimSuffix(profile, "\n"), "\n")
	for a, b := 1, len(lines)-1; a < b; a, b = a+1, b-1 {
		lines[a], lines[b] = lines[b], lines[a]
	}
	if err := os.WriteFile(input, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-input", input, "-output", output, "-filtered", filtered}, io.Discard); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("unstable normalized output: %v", err)
	}
}

func TestRunThresholdsMissingModulesAndWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statements string
		pkg        string
		minimum    string
		wantError  bool
	}{
		{"exact floor", "900", "example.test/module", "90", false},
		{"below floor", "899", "example.test/module", "90", true},
		{"rounded floor preserved", "8996", "example.test/module", "90", false},
		{"missing module", "900", "example.test/missing", "90", true},
		{"invalid minimum", "900", "example.test/module", "NaN", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input.out")
			missed := "100"
			if tc.name == "below floor" {
				missed = "101"
			}
			if tc.name == "rounded floor preserved" {
				missed = "1004"
			}
			profile := "mode: atomic\nexample.test/module/a.go:1.1,2.2 " + tc.statements + " 1\n" +
				"example.test/module/a.go:3.1,4.2 " + missed + " 0\n"
			if err := os.WriteFile(input, []byte(profile), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"-input", input, "-output", filepath.Join(dir, "merged.out"),
				"-filtered", filepath.Join(dir, "filtered.out"), "-minimum", tc.minimum, tc.pkg}
			var report bytes.Buffer
			if err := run(args, &report); (err != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%v report=%s", err, tc.wantError, &report)
			}
			if err := run([]string{"-input", input, "-output", input, "-filtered", args[5]}, io.Discard); err == nil {
				t.Fatal("input overwrite accepted")
			}
			if err := run([]string{"-input", input, "-output", filepath.Join(dir, "missing", "out"),
				"-filtered", args[5]}, io.Discard); err == nil {
				t.Fatal("write failure ignored")
			}
		})
	}
}

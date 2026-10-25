package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCollectRejectsProducerAndOutputErrors(t *testing.T) {
	dir := t.TempDir()
	source := `package main
import ("fmt"; "os"; "strings")
func main() {
    mode := os.Getenv("COVERAGE_PRODUCER_MODE")
    if mode == "missing" { return }
    for _, arg := range os.Args[1:] {
        if !strings.HasPrefix(arg, "-coverprofile=") { continue }
        f, err := os.Create(strings.TrimPrefix(arg, "-coverprofile="))
        if err != nil { panic(err) }
        fmt.Fprintln(f, "mode: atomic")
        if mode == "malformed" {
            fmt.Fprintln(f, "broken record")
            // More than a pipe buffer: rejected data must still be drained.
            for n := 0; n < 1024; n++ {
                if _, err := fmt.Fprintln(f, strings.Repeat("x", 4096)); err != nil { return }
            }
        }
        fmt.Fprintln(f, "example.test/module/a.go:1.1,2.2 1 3000000000")
        fmt.Fprintln(f, "example.test/module/a.go:1.1,2.2 1 3000000000")
        f.Close()
    }
    if mode == "failure" { os.Exit(7) }
}
`
	filename := filepath.Join(dir, "producer.go")
	if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	producer := filepath.Join(dir, "go")
	if runtime.GOOS == "windows" {
		producer += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", producer, filename).CombinedOutput(); err != nil {
		t.Fatalf("build producer: %v\n%s", err, output)
	}
	t.Setenv("PATH", dir)
	for _, mode := range []string{"missing", "malformed", "failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("COVERAGE_PRODUCER_MODE", mode)
			output := filepath.Join(dir, mode+".out")
			err := collect([]string{"-output", output, "example.test/module"}, io.Discard, io.Discard)
			if mode != "success" {
				if err == nil {
					t.Fatal("invalid or failed producer accepted")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("failed producer published a profile")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(output)
			if err != nil || string(raw) != "mode: atomic\nexample.test/module/a.go:1.1,2.2 1 6000000000\n" {
				t.Fatalf("collector changed exact counts: %v %s", err, raw)
			}
			if err := collect([]string{"-output", filepath.Join(dir, "missing", "out"), "example.test/module"}, io.Discard, io.Discard); err == nil {
				t.Fatal("output failure ignored")
			}
		})
	}
	t.Setenv("PATH", filepath.Join(dir, "missing"))
	if err := collect([]string{"-output", filepath.Join(dir, "absent.out"), "example.test/module"}, io.Discard, io.Discard); err == nil {
		t.Fatal("missing Go executable accepted")
	}
}

func TestCollectPreservesGoTestCountsAndFailure(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.test/coveragefixture\n\ngo 1.26.2\n",
		"core/core.go": `package core
func Branch(v bool) int { if v { return 1 }; return 2 }
func ChildOnly() int { return 3 }
`,
		"core/core_test.go": `package core
import ("os"; "os/exec"; "testing")
func TestMain(m *testing.M) {
    if os.Getenv("COVERAGE_FIXTURE_CHILD") == "1" { os.Exit(ChildOnly()-3) }
    os.Exit(m.Run())
}
func TestCore(t *testing.T) {
    if Branch(true) != 1 { t.Fatal("branch") }
    cmd := exec.Command(os.Args[0])
    cmd.Env = append(os.Environ(), "COVERAGE_FIXTURE_CHILD=1")
    if output, err := cmd.CombinedOutput(); err != nil { t.Fatalf("child: %v %s", err, output) }
    if os.Getenv("COVERAGE_FIXTURE_FAIL") == "1" { t.Fatal("intentional fixture failure") }
}
`,
		"peer/peer.go": `package peer
import "example.test/coveragefixture/core"
func Other() int { return core.Branch(false) }
`,
		"peer/peer_test.go": `package peer
import "testing"
func TestPeer(t *testing.T) { if Other() != 2 { t.Fatal("peer") } }
`,
		"orphan/orphan.go": "package orphan\nfunc Uncalled() int { return 42 }\n",
	}
	for name, source := range files {
		filename := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packages := []string{"example.test/coveragefixture/core", "example.test/coveragefixture/peer", "example.test/coveragefixture/orphan"}
	reference := filepath.Join(dir, "reference.out")
	cmd := exec.Command("go", append([]string{"test", "-timeout=60m", "-count=1", "-covermode=atomic",
		"-coverpkg=" + strings.Join(packages, ","), "-coverprofile=" + reference}, packages...)...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reference go test: %v\n%s", err, output)
	}
	output := filepath.Join(dir, "collected profile.out")
	args := append([]string{"-dir", dir, "-output", output}, packages...)
	var logs bytes.Buffer
	if err := collect(args, &logs, &logs); err != nil {
		t.Fatalf("collect: %v\n%s", err, &logs)
	}
	read := func(filename string) map[string]block {
		t.Helper()
		file, err := os.Open(filename)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		blocks, err := readBlocks(file)
		if err != nil {
			t.Fatal(err)
		}
		return blocks
	}
	want, got := read(reference), read(output)
	if len(got) != len(want) {
		t.Fatalf("block set changed: got %v want %v", got, want)
	}
	var orphan, child, summed bool
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s: got %+v want %+v", key, got[key], value)
		}
		orphan = orphan || strings.Contains(key, "/orphan/") && value.count == 0 && value.statements > 0
		child = child || strings.Contains(key, "/core/core.go:3.") && value.count > 0
		summed = summed || value.count >= 2
	}
	if !orphan || !child || !summed {
		t.Fatalf("fixture did not exercise orphan=%v subprocess=%v summed=%v: %v", orphan, child, summed, want)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != len(want)+1 || !strings.Contains(logs.String(), "ok") {
		t.Fatalf("not compact or test logs lost: %s\n%s", raw, &logs)
	}
	t.Setenv("COVERAGE_FIXTURE_FAIL", "1")
	logs.Reset()
	if err := collect(args, &logs, &logs); err == nil || !strings.Contains(logs.String(), "intentional fixture failure") {
		t.Fatalf("test failure suppressed: %v\n%s", err, &logs)
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("failed run published a partial profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "peer/peer.go"), []byte("invalid Go source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collect(args, io.Discard, io.Discard); err == nil {
		t.Fatal("build failure suppressed")
	}
	if err := collect([]string{"-output", output}, io.Discard, io.Discard); err == nil {
		t.Fatal("empty package list accepted")
	}
}

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

func TestReadBlocksPreservesCountsBeyondUint32(t *testing.T) {
	profile := "mode: atomic\n" +
		"example.test/module/a.go:1.1,2.2 1 3000000000\n" +
		"example.test/module/a.go:1.1,2.2 1 3000000000\n"
	blocks, err := readBlocks(strings.NewReader(profile))
	if err != nil || blocks["example.test/module/a.go:1.1,2.2"].count != 6000000000 {
		t.Fatalf("cross-test count saturated: %v %v", blocks, err)
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

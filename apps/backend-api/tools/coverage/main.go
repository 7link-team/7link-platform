// Command coverage compacts cross-package atomic coverage and checks module floors.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

type block struct {
	statements int64
	count      int64
}

type total struct {
	statements int64
	covered    int64
}

func main() {
	args := os.Args[1:]
	var err error
	if len(args) > 0 && args[0] == "collect" {
		err = collect(args[1:], os.Stdout, os.Stderr)
	} else {
		err = run(args, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// collect preserves go test's own per-test coverage processing, including
// auxiliary zero-hit packages and subprocess counters. Only its final duplicate
// text stream is intercepted; shared binary coverage directories lose those
// semantics. Linux can reopen the dedicated inherited pipe through /dev/fd/3.
// Other systems compact a temporary regular profile before publishing it.
func collect(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("collect", flag.ContinueOnError)
	dir := flags.String("dir", ".", "Go module working directory")
	output := flags.String("output", "", "compacted unfiltered profile")
	if err := flags.Parse(args); err != nil {
		return err
	}
	packages := flags.Args()
	if *output == "" || len(packages) == 0 {
		return errors.New("output and test packages are required")
	}
	cmd := exec.Command("go", "test", "-timeout=60m", "-count=1",
		"-covermode=atomic", "-coverpkg="+strings.Join(packages, ","))
	cmd.Dir = *dir
	cmd.Stdout, cmd.Stderr = stdout, stderr
	var blocks map[string]block
	if runtime.GOOS == "linux" {
		reader, writer, err := os.Pipe()
		if err != nil {
			return err
		}
		defer reader.Close()
		defer writer.Close()
		cmd.ExtraFiles = []*os.File{writer}
		cmd.Args = append(cmd.Args, "-coverprofile=/dev/fd/3")
		cmd.Args = append(cmd.Args, packages...)
		if err := cmd.Start(); err != nil {
			return err
		}
		// Only the child keeps a writer, so EOF also arrives on test failure.
		closeErr := writer.Close()
		blocks, err = readBlocks(reader)
		if err != nil {
			// os.Create opens /dev/fd/3 read-write, so the producer itself
			// retains a read end. Closing ours alone cannot unblock writes.
			// Drain rejected data so the original test command can finish.
			_, drainErr := io.Copy(io.Discard, reader)
			err = errors.Join(err, drainErr)
		}
		readCloseErr := reader.Close()
		if err := errors.Join(err, closeErr, readCloseErr, cmd.Wait()); err != nil {
			return err
		}
	} else {
		// Keep the compatibility profile on the requested output's volume.
		tempDir, err := os.MkdirTemp(filepath.Dir(*output), ".coverage-collect-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tempDir)
		profile := filepath.Join(tempDir, "raw.out")
		cmd.Args = append(cmd.Args, "-coverprofile="+profile)
		cmd.Args = append(cmd.Args, packages...)
		if err := cmd.Run(); err != nil {
			return err
		}
		file, err := os.Open(profile)
		if err != nil {
			return err
		}
		blocks, err = readBlocks(file)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(blocks))
	for key := range blocks {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return writeProfile(*output, keys, blocks, false)
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("coverage", flag.ContinueOnError)
	input := flags.String("input", "", "raw atomic coverage profile")
	output := flags.String("output", "", "compacted unfiltered profile")
	filtered := flags.String("filtered", "", "compacted profile for total coverage")
	minimum := flags.Float64("minimum", 90, "minimum rounded module coverage percentage")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if math.IsNaN(*minimum) || *minimum < 0 || *minimum > 100 {
		return errors.New("minimum must be between 0 and 100")
	}
	paths := map[string]bool{}
	for _, name := range []string{*input, *output, *filtered} {
		if name == "" {
			return errors.New("input, output and filtered paths are required")
		}
		absolute, err := filepath.Abs(name)
		if err != nil {
			return err
		}
		if paths[absolute] {
			return errors.New("input, output and filtered paths must be distinct")
		}
		paths[absolute] = true
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	blocks, readErr := readBlocks(file)
	err = errors.Join(readErr, file.Close())
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(blocks))
	totals := map[string]total{}
	for key, value := range blocks {
		keys = append(keys, key)
		filename := key[:strings.LastIndexByte(key, ':')]
		pkg := path.Dir(filename)
		t := totals[pkg]
		t.statements += value.statements
		if value.count > 0 {
			t.covered += value.statements
		}
		totals[pkg] = t
	}
	slices.Sort(keys)
	if err := writeProfile(*output, keys, blocks, false); err != nil {
		return err
	}
	if err := writeProfile(*filtered, keys, blocks, true); err != nil {
		return err
	}
	failed := false
	for _, pkg := range flags.Args() {
		t := totals[pkg]
		if t.statements == 0 {
			fmt.Fprintf(stdout, "module coverage: %-48s MISSING\n", pkg)
			failed = true
			continue
		}
		// The existing gate compares its one-decimal report, not the raw ratio.
		percentage := fmt.Sprintf("%.1f", float64(t.covered)*100/float64(t.statements))
		rounded, _ := strconv.ParseFloat(percentage, 64)
		fmt.Fprintf(stdout, "module coverage: %-48s %s%% (minimum %.1f%%)\n", pkg, percentage, *minimum)
		failed = failed || rounded < *minimum
	}
	if failed {
		return errors.New("module coverage below minimum or missing")
	}
	return nil
}

func readBlocks(reader io.Reader) (map[string]block, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() || scanner.Text() != "mode: atomic" {
		return nil, errors.New("expected mode: atomic coverage profile")
	}
	location := regexp.MustCompile(`^.+:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$`)
	blocks := map[string]block{}
	for line := 2; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid coverage record at line %d", line)
		}
		statements, stmtErr := strconv.ParseInt(fields[1], 10, 64)
		count, countErr := strconv.ParseInt(fields[2], 10, 64)
		if stmtErr != nil || countErr != nil || statements < 0 || count < 0 {
			return nil, fmt.Errorf("invalid coverage numbers at line %d", line)
		}
		old, exists := blocks[fields[0]]
		if !exists && !location.MatchString(fields[0]) {
			return nil, fmt.Errorf("invalid coverage location at line %d", line)
		}
		if exists && old.statements != statements {
			return nil, fmt.Errorf("inconsistent statement count at line %d", line)
		}
		if count > math.MaxInt64-old.count {
			return nil, fmt.Errorf("coverage count overflow at line %d", line)
		}
		blocks[fields[0]] = block{statements: statements, count: old.count + count}
	}
	return blocks, scanner.Err()
}

func writeProfile(filename string, keys []string, blocks map[string]block, filter bool) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	_, writeErr := fmt.Fprintln(writer, "mode: atomic")
	for _, key := range keys {
		if writeErr != nil {
			break
		}
		if filter && excluded(key) {
			continue
		}
		b := blocks[key]
		_, writeErr = fmt.Fprintf(writer, "%s %d %d\n", key, b.statements, b.count)
	}
	return errors.Join(writeErr, writer.Flush(), file.Close())
}

func excluded(key string) bool {
	return strings.Contains(key, "/cmd/7link-api/main.go:") ||
		strings.Contains(key, "/cmd/infra-api/main.go:") ||
		strings.Contains(key, ".gen.go:") || strings.Contains(key, "/query/gen.go:")
}

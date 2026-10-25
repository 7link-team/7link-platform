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
	"path"
	"path/filepath"
	"regexp"
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
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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

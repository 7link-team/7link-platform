package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	lockVersion            = 1
	expectedIAMTables      = 81
	expectedCrossModuleFKs = 37
)

var (
	expectedModules = []string{
		"audit", "authn", "core", "federation", "governance",
		"identity", "oauth", "organization", "provisioning", "webhook",
	}
	expectedDialects = []string{"mysql", "postgres", "sqlite"}
	createTableRE    = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)`)
	referenceRE      = regexp.MustCompile(`(?i)\bREFERENCES\s+([^\s(]+)`)
	constraintRE     = regexp.MustCompile(`(?i)\bCONSTRAINT\s+([^\s;,(]+)`)
	createIndexRE    = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+([^\s;(]+)`)
	createTriggerRE  = regexp.MustCompile(`(?i)\bCREATE\s+TRIGGER\s+([^\s;(]+)`)
	inlineKeyRE      = regexp.MustCompile(`(?im)^\s*(?:UNIQUE\s+)?KEY\s+([^\s(]+)`)
	timeColumnRE     = regexp.MustCompile(`(?im)^\s*([a-z][a-z0-9_]*(?:_at|_time|_before|_after))\s+([a-z]+(?:\([0-9]+\))?)[^,\n]*`)
	// timeTypeRE keys on the declared type instead of the column name, and is not
	// anchored to the start of a line. timeColumnRE above misses two things a
	// probe confirmed: a time column whose name ends in something other than the
	// four known suffixes (locked_until), and any column added by ALTER TABLE ...
	// ADD COLUMN, because that declaration does not begin a line.
	timeTypeRE    = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_]*)\s+(TIMESTAMPTZ|TIMESTAMP|DATETIME)\s*(\(\s*[0-9]+\s*\))?`)
	emptyBigIntRE = regexp.MustCompile(`(?i)\bBIGINT\b[^,\n]*\bDEFAULT\s*(?:\(\s*)?''`)
	sha256RE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type migrationFile struct {
	path     string
	module   string
	dialect  string
	filename string
	contents string
	hash     string
}

type schemaLock struct {
	Version int         `json:"version"`
	Entries []lockEntry `json:"entries"`
}

type lockEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type staticSummary struct {
	files          int
	tables         int
	crossModuleFKs int
}

func main() {
	target := flag.String("target", ".", "backend-api root")
	write := flag.Bool("write", false, "write or extend the lock after a successful real-MySQL gate")
	flag.Parse()

	root, err := filepath.Abs(*target)
	fatalIf(err)
	files, err := loadMigrationFiles(filepath.Join(root, "internal", "iam"))
	fatalIf(err)
	summary, err := validateStatic(files)
	fatalIf(err)
	fatalIf(validateNoMySQLTimestamp(filepath.Join(root, "internal")))

	lockPath := filepath.Join(root, "tests", "migration", "schema.lock")
	if *write {
		if existing, loadErr := loadLock(lockPath); loadErr == nil {
			fatalIf(verifyLock(existing, files))
		} else if !errors.Is(loadErr, os.ErrNotExist) {
			fatalIf(loadErr)
		}
		fatalIf(writeLock(lockPath, files))
		fmt.Printf("schema lock: wrote %s\n", lockPath)
	} else {
		lock, loadErr := loadLock(lockPath)
		if errors.Is(loadErr, os.ErrNotExist) {
			fatalIf(fmt.Errorf("schema lock is missing: run with --write only after the real MySQL 8 gate passes"))
		}
		fatalIf(loadErr)
		fatalIf(verifyLock(lock, files))
		fmt.Printf("schema lock: %d published migration files unchanged\n", len(lock.Entries))
	}
	fmt.Printf("static reconciliation: %d files, %d IAM tables, %d cross-module foreign keys\n",
		summary.files, summary.tables, summary.crossModuleFKs)
}

func fatalIf(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func loadMigrationFiles(iamRoot string) ([]migrationFile, error) {
	var files []migrationFile
	err := filepath.WalkDir(iamRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".sql" {
			return walkErr
		}
		relative, err := filepath.Rel(iamRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		parts := strings.Split(relative, "/")
		if len(parts) != 4 || parts[1] != "sql" || !slices.Contains(expectedDialects, parts[2]) {
			return fmt.Errorf("unexpected IAM migration path %q", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")
		digest := sha256.Sum256([]byte(normalized))
		files = append(files, migrationFile{
			path: relative, module: parts[0], dialect: parts[2], filename: parts[3],
			contents: normalized, hash: hex.EncodeToString(digest[:]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	if len(files) == 0 {
		return nil, errors.New("no IAM migration files found")
	}
	return files, nil
}

func validateStatic(files []migrationFile) (staticSummary, error) {
	if err := validateFileSets(files); err != nil {
		return staticSummary{}, err
	}
	owners := make(map[string]string, expectedIAMTables)
	stripped := make(map[string]string, len(files))
	for _, file := range files {
		contents, err := stripSQLComments(file.contents)
		if err != nil {
			return staticSummary{}, fmt.Errorf("%s: %w", file.path, err)
		}
		stripped[file.path] = contents
		if err := validateObjectPrefixes(file.path, contents); err != nil {
			return staticSummary{}, err
		}
		if emptyBigIntRE.MatchString(contents) {
			return staticSummary{}, fmt.Errorf("%s: BIGINT uses an empty-string default", file.path)
		}
		expectedTimeType := map[string]string{
			"mysql": "DATETIME(3)", "postgres": "TIMESTAMPTZ(3)", "sqlite": "DATETIME",
		}[file.dialect]
		for _, match := range timeColumnRE.FindAllStringSubmatch(contents, -1) {
			if !strings.EqualFold(match[2], expectedTimeType) {
				return staticSummary{}, fmt.Errorf("%s: %s time column %s uses %s instead of %s",
					file.path, file.dialect, match[1], match[2], expectedTimeType)
			}
			declaration := strings.ToUpper(match[0])
			if strings.Contains(declaration, "DEFAULT 0") || strings.Contains(declaration, "DEFAULT '0'") {
				return staticSummary{}, fmt.Errorf("%s: MySQL time column %s uses a zero sentinel", file.path, match[1])
			}
		}
		for _, match := range timeTypeRE.FindAllStringSubmatch(contents, -1) {
			declared := strings.ToUpper(match[2] + strings.ReplaceAll(match[3], " ", ""))
			if !strings.EqualFold(declared, expectedTimeType) {
				return staticSummary{}, fmt.Errorf("%s: %s column %s is declared %s instead of %s",
					file.path, file.dialect, match[1], declared, expectedTimeType)
			}
		}
		if file.dialect != "mysql" {
			continue
		}
		for _, match := range createTableRE.FindAllStringSubmatch(contents, -1) {
			table, ok := prefixedIAMName(match[1])
			if !ok {
				continue
			}
			if previous, duplicate := owners[table]; duplicate {
				return staticSummary{}, fmt.Errorf("MySQL table %s is created by both %s and %s", table, previous, file.module)
			}
			owners[table] = file.module
		}
	}
	if len(owners) != expectedIAMTables {
		return staticSummary{}, fmt.Errorf("MySQL IAM table count is %d, expected %d", len(owners), expectedIAMTables)
	}

	crossModuleFKs := 0
	for _, file := range files {
		if file.dialect != "mysql" {
			continue
		}
		for _, match := range referenceRE.FindAllStringSubmatch(stripped[file.path], -1) {
			table, ok := prefixedIAMName(match[1])
			if !ok {
				continue
			}
			owner, exists := owners[table]
			if !exists {
				return staticSummary{}, fmt.Errorf("%s: reference target %s has no MySQL owner", file.path, table)
			}
			if owner != file.module {
				crossModuleFKs++
			}
		}
	}
	if crossModuleFKs != expectedCrossModuleFKs {
		return staticSummary{}, fmt.Errorf("MySQL cross-module foreign-key count is %d, expected %d", crossModuleFKs, expectedCrossModuleFKs)
	}
	return staticSummary{files: len(files), tables: len(owners), crossModuleFKs: crossModuleFKs}, nil
}

// validateNoMySQLTimestamp rejects MySQL TIMESTAMP columns in every module, not
// only the IAM tree the rest of this tool reconciles. MySQL caps TIMESTAMP at
// 2038-01-19, so such a column behaves normally for current dates and then
// refuses a long expiry, a contract end date, or a 9999-12-31 never-expires
// sentinel. DATETIME reaches 9999 and matches what the PostgreSQL side stores.
//
// Only this one rule is applied repository-wide: the exact-type reconciliation
// above stays IAM-scoped because published non-IAM migrations legitimately
// declare TIMESTAMPTZ without a precision, and rewriting a published migration
// is prohibited.
func validateNoMySQLTimestamp(internalRoot string) error {
	return filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".sql" {
			return walkErr
		}
		if filepath.Base(filepath.Dir(path)) != "mysql" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents, err := stripSQLComments(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, match := range timeTypeRE.FindAllStringSubmatch(contents, -1) {
			if strings.EqualFold(match[2], "TIMESTAMP") {
				return fmt.Errorf("%s: MySQL column %s uses TIMESTAMP, which stops at 2038-01-19; use DATETIME",
					path, match[1])
			}
		}
		return nil
	})
}

func validateFileSets(files []migrationFile) error {
	sets := make(map[string]map[string][]string)
	for _, file := range files {
		if sets[file.module] == nil {
			sets[file.module] = make(map[string][]string)
		}
		sets[file.module][file.dialect] = append(sets[file.module][file.dialect], file.filename)
	}
	modules := make([]string, 0, len(sets))
	for moduleName := range sets {
		modules = append(modules, moduleName)
	}
	sort.Strings(modules)
	if !slices.Equal(modules, expectedModules) {
		return fmt.Errorf("IAM migration modules are %v, expected %v", modules, expectedModules)
	}
	for _, moduleName := range modules {
		baseline := sets[moduleName][expectedDialects[0]]
		sort.Strings(baseline)
		for _, dialect := range expectedDialects[1:] {
			filesForDialect := sets[moduleName][dialect]
			sort.Strings(filesForDialect)
			if !slices.Equal(baseline, filesForDialect) {
				return fmt.Errorf("%s migration files differ between mysql and %s", moduleName, dialect)
			}
		}
	}
	return nil
}

func validateObjectPrefixes(path, contents string) error {
	checks := []struct {
		kind    string
		pattern *regexp.Regexp
	}{
		{kind: "table", pattern: createTableRE},
		{kind: "constraint", pattern: constraintRE},
		{kind: "index", pattern: createIndexRE},
		{kind: "trigger", pattern: createTriggerRE},
		{kind: "key", pattern: inlineKeyRE},
	}
	for _, check := range checks {
		for _, match := range check.pattern.FindAllStringSubmatch(contents, -1) {
			if strings.HasPrefix(match[1], "{{.Prefix}}") {
				continue
			}
			return fmt.Errorf("%s: %s name %q is not deployment-prefixed", path, check.kind, match[1])
		}
	}
	for _, match := range referenceRE.FindAllStringSubmatch(contents, -1) {
		if strings.HasPrefix(match[1], "{{.Prefix}}") {
			continue
		}
		return fmt.Errorf("%s: reference target %q is not deployment-prefixed", path, match[1])
	}
	return nil
}

func prefixedIAMName(token string) (string, bool) {
	const prefix = "{{.Prefix}}"
	if !strings.HasPrefix(token, prefix+"iam_") {
		return "", false
	}
	return strings.ToLower(strings.Trim(token[len(prefix):], "`\"")), true
}

func loadLock(path string) (schemaLock, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return schemaLock{}, err
	}
	var lock schemaLock
	if err := json.Unmarshal(contents, &lock); err != nil {
		return schemaLock{}, fmt.Errorf("decode schema lock: %w", err)
	}
	if lock.Version != lockVersion {
		return schemaLock{}, fmt.Errorf("unsupported schema lock version %d", lock.Version)
	}
	return lock, nil
}

func writeLock(path string, files []migrationFile) error {
	lock := schemaLock{Version: lockVersion, Entries: make([]lockEntry, len(files))}
	for index, file := range files {
		lock.Entries[index] = lockEntry{Path: file.path, SHA256: file.hash}
	}
	contents, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return os.WriteFile(path, contents, 0o644)
}

func verifyLock(lock schemaLock, files []migrationFile) error {
	if len(lock.Entries) == 0 {
		return errors.New("schema lock contains no published migrations")
	}
	actual := make(map[string]migrationFile, len(files))
	for _, file := range files {
		actual[file.path] = file
	}
	locked := make(map[string]struct{}, len(lock.Entries))
	maxVersion := make(map[string]int)
	for _, entry := range lock.Entries {
		if !sha256RE.MatchString(entry.SHA256) {
			return fmt.Errorf("schema lock contains invalid SHA-256 for %s", entry.Path)
		}
		if _, duplicate := locked[entry.Path]; duplicate {
			return fmt.Errorf("schema lock contains duplicate path %s", entry.Path)
		}
		locked[entry.Path] = struct{}{}
		file, exists := actual[entry.Path]
		if !exists {
			return fmt.Errorf("published migration was deleted: %s", entry.Path)
		}
		if file.hash != entry.SHA256 {
			return fmt.Errorf("published migration was modified: %s", entry.Path)
		}
		version, key, err := migrationVersion(entry.Path)
		if err != nil {
			return err
		}
		maxVersion[key] = max(maxVersion[key], version)
	}
	for _, file := range files {
		if _, exists := locked[file.path]; exists {
			continue
		}
		version, key, err := migrationVersion(file.path)
		if err != nil {
			return err
		}
		if version <= maxVersion[key] {
			return fmt.Errorf("migration %s was inserted at version %d; published maximum is %d", file.path, version, maxVersion[key])
		}
	}
	return nil
}

func migrationVersion(path string) (int, string, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) != 4 {
		return 0, "", fmt.Errorf("invalid migration path %s", path)
	}
	separator := strings.IndexByte(parts[3], '_')
	if separator <= 0 {
		return 0, "", fmt.Errorf("migration filename has no numeric prefix: %s", path)
	}
	version, err := strconv.Atoi(parts[3][:separator])
	if err != nil {
		return 0, "", fmt.Errorf("migration filename has invalid version: %s", path)
	}
	return version, parts[0] + "/" + parts[2], nil
}

func stripSQLComments(contents string) (string, error) {
	var output strings.Builder
	output.Grow(len(contents))
	for index := 0; index < len(contents); {
		switch {
		case contents[index] == '\'' || contents[index] == '"' || contents[index] == '`':
			quote := contents[index]
			output.WriteByte(quote)
			index++
			closed := false
			for index < len(contents) {
				current := contents[index]
				output.WriteByte(current)
				index++
				if current == '\\' && index < len(contents) {
					output.WriteByte(contents[index])
					index++
					continue
				}
				if current != quote {
					continue
				}
				if index < len(contents) && contents[index] == quote {
					output.WriteByte(contents[index])
					index++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return "", errors.New("unterminated quoted SQL value")
			}
		case contents[index] == '#' || isDashComment(contents, index):
			for index < len(contents) && contents[index] != '\n' {
				index++
			}
		case index+1 < len(contents) && contents[index:index+2] == "/*":
			index += 2
			closed := false
			for index+1 < len(contents) {
				if contents[index:index+2] == "*/" {
					index += 2
					closed = true
					break
				}
				if contents[index] == '\n' {
					output.WriteByte('\n')
				}
				index++
			}
			if !closed {
				return "", errors.New("unterminated SQL block comment")
			}
		default:
			output.WriteByte(contents[index])
			index++
		}
	}
	return output.String(), nil
}

func isDashComment(contents string, index int) bool {
	if index+1 >= len(contents) || contents[index:index+2] != "--" {
		return false
	}
	if index+2 == len(contents) {
		return true
	}
	next := contents[index+2]
	return next == ' ' || next == '\t' || next == '\r' || next == '\n'
}

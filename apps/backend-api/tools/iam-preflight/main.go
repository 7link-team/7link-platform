package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
)

const (
	expectedServerTree = "201052baddd745d1c2727cf29bbc3c68253a1af0"
	defaultDockerSSH   = "root@10.0.0.100"
	mysqlImage         = "mysql:8.4@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb"
)

var iamModules = []string{
	"audit", "authn", "federation", "governance", "iam", "identity", "oauth", "organization", "provisioning",
}

type options struct {
	target          string
	source          string
	dockerSSH       string
	sshKey          string
	fix             bool
	bootstrapDocker bool
}

type dockerEnvironment struct {
	command string
	host    string
	version string
}

func main() {
	var opts options
	flag.StringVar(&opts.target, "target", ".", "backend-api directory")
	flag.StringVar(&opts.source, "source", os.Getenv("IAM_SOURCE_ROOT"), "chaosplus apps/server directory")
	flag.StringVar(&opts.dockerSSH, "docker-ssh", envOrDefault("IAM_DOCKER_SSH", defaultDockerSSH), "SSH target for remote Docker")
	flag.StringVar(&opts.sshKey, "ssh-key", os.Getenv("IAM_DOCKER_SSH_KEY"), "identity file for remote Docker")
	flag.BoolVar(&opts.fix, "fix", false, "apply safe machine-local fixes")
	flag.BoolVar(&opts.bootstrapDocker, "bootstrap-docker", false, "install docker.io on the configured Debian SSH host when absent")
	flag.Parse()

	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "iam-preflight: %v\n", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	target, err := filepath.Abs(opts.target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}

	if opts.fix && runtime.GOOS == "windows" {
		if err := fixWindowsCheckout(target); err != nil {
			return err
		}
	}

	source, err := resolveSource(opts.source, runtime.GOOS)
	if err != nil {
		return err
	}
	commit, err := verifySource(source)
	if err != nil {
		return err
	}
	if err := verifySchemaTables(source); err != nil {
		return err
	}

	dockerEnv, err := resolveDocker(opts)
	if err != nil {
		return err
	}
	if err := writeEnvironment(target, source, commit, dockerEnv); err != nil {
		return err
	}

	fmt.Printf("source: %s\nsource commit: %s\nsource tree: %s\ndocker: %s (%s)\ndocker test host: %s\nmysql image: %s\nenvironment: %s\n",
		source, commit, expectedServerTree, dockerEnv.command, dockerEnv.version, dockerEnv.host,
		mysqlImage,
		filepath.Join(target, ".temp", "iam-env.mk"))
	return nil
}

func resolveSource(override, goos string) (string, error) {
	candidates := sourceCandidates(override, goos)
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", fmt.Errorf("resolve source %q: %w", candidate, absErr)
			}
			return absolute, nil
		}
	}
	return "", fmt.Errorf("source apps/server not found; checked %s; set IAM_SOURCE_ROOT or pass --source", strings.Join(candidates, ", "))
}

func sourceCandidates(override, goos string) []string {
	if override != "" {
		return []string{override}
	}
	if goos == "darwin" {
		return []string{"/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server"}
	}
	if goos == "windows" {
		return []string{`D:\Projects\Chaosplus\chaosplus-api\apps\server`}
	}
	return []string{
		"/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server",
		`D:\Projects\Chaosplus\chaosplus-api\apps\server`,
	}
}

func verifySource(source string) (string, error) {
	for _, module := range iamModules {
		path := filepath.Join(source, "internal", "modules", module)
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return "", fmt.Errorf("source module missing: %s", path)
		}
	}

	repoRoot, err := commandOutput("git", "-C", source, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve source repository: %w", err)
	}
	relative, err := filepath.Rel(strings.TrimSpace(repoRoot), source)
	if err != nil {
		return "", fmt.Errorf("resolve source tree path: %w", err)
	}
	status, err := commandOutput("git", "-C", source, "status", "--porcelain", "--", ".")
	if err != nil {
		return "", fmt.Errorf("inspect source worktree: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return "", fmt.Errorf("source apps/server has uncommitted changes:\n%s", strings.TrimSpace(status))
	}
	tree, err := commandOutput("git", "-C", source, "rev-parse", "HEAD:"+filepath.ToSlash(relative))
	if err != nil {
		return "", fmt.Errorf("read source tree: %w", err)
	}
	if strings.TrimSpace(tree) != expectedServerTree {
		return "", fmt.Errorf("source apps/server tree is %s, expected %s; review source changes before migration",
			strings.TrimSpace(tree), expectedServerTree)
	}
	commit, err := commandOutput("git", "-C", source, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read source commit: %w", err)
	}
	return strings.TrimSpace(commit), nil
}

func verifySchemaTables(source string) error {
	sets := make(map[string][]string, 3)
	for _, dialect := range []string{"mysql", "postgres", "sqlite"} {
		tables, err := schemaTables(source, dialect)
		if err != nil {
			return err
		}
		if len(tables) != 63 {
			return fmt.Errorf("%s schema creates %d unique tables, expected 63", dialect, len(tables))
		}
		sets[dialect] = tables
	}
	if !slices.Equal(sets["mysql"], sets["postgres"]) || !slices.Equal(sets["mysql"], sets["sqlite"]) {
		return errors.New("mysql, postgres, and sqlite IAM table sets differ")
	}
	return nil
}

func schemaTables(source, dialect string) ([]string, error) {
	tablePattern := regexp.MustCompile("(?i)\\bCREATE\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?[`\"]?([a-z][a-z0-9_]*)")
	tableSet := make(map[string]struct{})
	err := filepath.WalkDir(filepath.Join(source, "internal", "modules"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".sql" {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		if !containsSequence(parts, "sql", dialect) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range tablePattern.FindAllSubmatch(content, -1) {
			tableSet[strings.ToLower(string(match[1]))] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s schema: %w", dialect, err)
	}
	tables := make([]string, 0, len(tableSet))
	for table := range tableSet {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables, nil
}

func containsSequence(parts []string, first, second string) bool {
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] == first && parts[index+1] == second {
			return true
		}
	}
	return false
}

func fixWindowsCheckout(target string) error {
	if err := runCommand(target, "git", "config", "--local", "core.autocrlf", "false"); err != nil {
		return fmt.Errorf("set repository core.autocrlf: %w", err)
	}
	output, err := commandOutputIn(target, "git", "ls-files", "--eol", "--", "*.go")
	if err != nil {
		return fmt.Errorf("inspect Go line endings: %w", err)
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "i/lf") || !strings.Contains(line, "w/crlf") {
			continue
		}
		_, file, found := strings.Cut(line, "\t")
		if !found {
			return fmt.Errorf("unexpected git ls-files --eol output: %q", line)
		}
		path := filepath.Join(target, filepath.FromSlash(file))
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		normalized := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		if writeErr := os.WriteFile(path, normalized, 0o644); writeErr != nil {
			return fmt.Errorf("normalize %s: %w", path, writeErr)
		}
		// Replacing CRLF changes the worktree stat information. When the
		// canonical content still equals the index, refresh that entry so Git
		// does not report hundreds of false modifications. Never stage a file
		// whose content actually differs.
		if commandSucceeds(target, "git", "diff", "--quiet", "--", file) {
			if addErr := runCommand(target, "git", "add", "--", file); addErr != nil {
				return fmt.Errorf("refresh normalized index entry %s: %w", file, addErr)
			}
		}
	}
	return nil
}

func resolveDocker(opts options) (dockerEnvironment, error) {
	if docker, err := exec.LookPath("docker"); err == nil {
		if version, versionErr := commandOutput(docker, "version", "--format", "{{.Server.Version}}"); versionErr == nil {
			if opts.fix {
				if pullErr := runCommand("", docker, "pull", mysqlImage); pullErr != nil {
					return dockerEnvironment{}, fmt.Errorf("pull pinned MySQL image: %w", pullErr)
				}
			}
			return dockerEnvironment{command: "docker", host: "127.0.0.1", version: strings.TrimSpace(version)}, nil
		}
	}
	if runtime.GOOS != "windows" {
		return dockerEnvironment{}, errors.New("local Docker is unavailable; install/start Docker or configure a supported remote")
	}

	key := opts.sshKey
	if key == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return dockerEnvironment{}, fmt.Errorf("resolve SSH key: %w", err)
		}
		key = filepath.Join(home, ".ssh", "iam-docker-10.0.0.100")
	}
	if _, err := os.Stat(key); err != nil {
		return dockerEnvironment{}, fmt.Errorf("remote Docker SSH key not found: %s", key)
	}
	sshArgs := []string{"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-i", key, opts.dockerSSH}
	version, err := commandOutput("ssh", append(sshArgs, "docker", "version", "--format", "{{.Server.Version}}")...)
	if err != nil && opts.bootstrapDocker {
		for _, command := range [][]string{
			{"apt-get", "update"},
			{"apt-get", "install", "-y", "docker.io"},
			{"systemctl", "enable", "--now", "docker"},
		} {
			if installErr := runCommand("", "ssh", append(sshArgs, command...)...); installErr != nil {
				return dockerEnvironment{}, fmt.Errorf("bootstrap remote Docker (%s): %w", command[0], installErr)
			}
		}
		version, err = commandOutput("ssh", append(sshArgs, "docker", "version", "--format", "{{.Server.Version}}")...)
	}
	if err != nil {
		return dockerEnvironment{}, fmt.Errorf("remote Docker unavailable through %s: %w", opts.dockerSSH, err)
	}
	if opts.fix {
		if pullErr := runCommand("", "ssh", append(sshArgs, "docker", "pull", mysqlImage)...); pullErr != nil {
			return dockerEnvironment{}, fmt.Errorf("pull pinned MySQL image through %s: %w", opts.dockerSSH, pullErr)
		}
	}
	host := opts.dockerSSH
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	key = filepath.ToSlash(key)
	command := fmt.Sprintf("ssh -o BatchMode=yes -o IdentitiesOnly=yes -i %s %s docker", makeEscape(key), makeEscape(opts.dockerSSH))
	return dockerEnvironment{command: command, host: host, version: strings.TrimSpace(version)}, nil
}

func writeEnvironment(target, source, commit string, dockerEnv dockerEnvironment) error {
	directory := filepath.Join(target, ".temp")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create environment directory: %w", err)
	}
	content := fmt.Sprintf("# Generated by make iam-preflight; do not commit.\nIAM_SOURCE_ROOT := %s\nIAM_SOURCE_COMMIT := %s\nIAM_SOURCE_TREE := %s\nDOCKER_CMD := %s\nDOCKER_TEST_HOST := %s\nMYSQL8_IMAGE := %s\n",
		makeEscape(filepath.ToSlash(source)), commit, expectedServerTree, dockerEnv.command, dockerEnv.host, mysqlImage)
	if err := os.WriteFile(filepath.Join(directory, "iam-env.mk"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write environment: %w", err)
	}
	return nil
}

func makeEscape(value string) string {
	return strings.NewReplacer("$", "$$", "#", "\\#", " ", "\\ ").Replace(value)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func commandOutput(name string, args ...string) (string, error) {
	return commandOutputIn("", name, args...)
}

func commandOutputIn(directory, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

func runCommand(directory, name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func commandSucceeds(directory, name string, args ...string) bool {
	command := exec.Command(name, args...)
	command.Dir = directory
	return command.Run() == nil
}

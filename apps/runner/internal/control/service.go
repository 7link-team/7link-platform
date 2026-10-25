package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Service manages a user-owned background daemon without acquiring its live profile lock.
func Service(ctx context.Context, action, profile string, out io.Writer) error {
	if action != "install" && action != "uninstall" && action != "status" {
		return errors.New("service requires install, uninstall or status")
	}
	dir, err := filepath.Abs(profile)
	if err != nil {
		return err
	}
	if action == "install" {
		store, err := OpenStore(dir)
		if err != nil {
			return err
		}
		_, err = store.Profile()
		store.Close()
		if err != nil {
			return errors.New("service install requires an enrolled profile")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return manageService(ctx, serviceConfig{OS: runtime.GOOS, Profile: dir, Executable: executable, Home: home, Path: os.Getenv("PATH"), UID: strconv.Itoa(os.Getuid())}, action, out, runServiceCommand)
}

type serviceConfig struct{ OS, Profile, Executable, Home, Path, UID string }
type serviceCommand func(context.Context, string, ...string) ([]byte, error)

func (c serviceConfig) name() string {
	profile := filepath.Clean(c.Profile)
	if c.OS == "windows" {
		profile = strings.ToLower(profile)
	}
	sum := sha256.Sum256([]byte(profile))
	return fmt.Sprintf("execution-runner-%x", sum[:12])
}

func runServiceCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideServiceWindow(cmd)
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("service manager command %s failed: %w", filepath.Base(name), err)
	}
	return data, nil
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func vbQuote(s string) string {
	return `"` + strings.NewReplacer(`"`, `""`, "\r", `" & vbCr & "`, "\n", `" & vbLf & "`).Replace(s) + `"`
}
func (c serviceConfig) launcherPath() string { return filepath.Join(c.Profile, c.name()+".vbs") }
func (c serviceConfig) launcher() []byte {
	command := `"` + c.Executable + `" daemon --profile "` + c.Profile + `"`
	script := "Set shell=CreateObject(\"WScript.Shell\")\r\nSet environment=shell.Environment(\"PROCESS\")\r\nenvironment(\"PATH\")=" + vbQuote(c.Path) + "\r\nWScript.Quit shell.Run(" + vbQuote(command) + ",0,True)\r\n"
	// Windows Script Host reads Unicode script files with a UTF-16LE BOM.
	data := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(script)) {
		data = append(data, byte(u), byte(u>>8))
	}
	return data
}

func windowsServiceScript(c serviceConfig, action string) string {
	prefix := "$ErrorActionPreference='Stop'; $s=New-Object -ComObject Schedule.Service; $s.Connect(); $f=$s.GetFolder('\\'); $name=" + psQuote(c.name()) + "; "
	if action == "status" {
		return prefix + "$t=$null; try {$t=$f.GetTask($name)} catch {if ($_.Exception.HResult -ne -2147024894) {throw}}; if ($null -eq $t) { @{installed=$false;running=$false}|ConvertTo-Json -Compress } else { @{installed=$true;running=($t.State -eq 4)}|ConvertTo-Json -Compress }"
	}
	if action == "uninstall" {
		return prefix + "$t=$null; try {$t=$f.GetTask($name)} catch {if ($_.Exception.HResult -ne -2147024894) {throw}}; if ($null -ne $t) {$launcher=" + psQuote(`"`+c.launcherPath()+`"`) + "; $processes=Get-CimInstance Win32_Process -Filter \"Name='wscript.exe'\" | Where-Object {$_.CommandLine -and ($_.CommandLine.IndexOf($launcher,[StringComparison]::OrdinalIgnoreCase) -ge 0)}; foreach ($p in $processes) { & ($env:SystemRoot+'\\System32\\taskkill.exe') /PID $p.ProcessId /T /F | Out-Null; if ($LASTEXITCODE -ne 0) {throw 'Unable to stop service process tree'} }; $t.Stop(0); $f.DeleteTask($name,0)}"
	}
	return prefix + "$d=$s.NewTask(0); $d.RegistrationInfo.Description='Execution runner daemon'; $d.Settings.Hidden=$true; $d.Settings.Enabled=$true; $d.Settings.DisallowStartIfOnBatteries=$false; $d.Settings.StopIfGoingOnBatteries=$false; $d.Settings.ExecutionTimeLimit='PT0S'; $d.Settings.MultipleInstances=2; $d.Principal.UserId=[System.Security.Principal.WindowsIdentity]::GetCurrent().Name; $d.Principal.LogonType=3; $d.Principal.RunLevel=0; $trigger=$d.Triggers.Create(9); $trigger.UserId=$d.Principal.UserId; $a=$d.Actions.Create(0); $a.Path=$env:SystemRoot+'\\System32\\wscript.exe'; $a.Arguments=" + psQuote(`//B //Nologo "`+c.launcherPath()+`"`) + "; $a.WorkingDirectory=" + psQuote(c.Profile) + "; $t=$f.RegisterTaskDefinition($name,$d,6,$d.Principal.UserId,$null,3); $null=$t.Run($null)"
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value) + `"`
}
func (c serviceConfig) unit() string {
	return "[Unit]\nDescription=Execution runner daemon\nAfter=network-online.target\n\n[Service]\nExecStart=:" + systemdQuote(c.Executable) + " daemon --profile " + systemdQuote(c.Profile) + "\nEnvironment=" + systemdQuote("PATH="+c.Path) + "\nRestart=on-failure\nRestartSec=10\nKillMode=control-group\n\n[Install]\nWantedBy=default.target\n"
}
func xmlText(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func (c serviceConfig) plist() string {
	return `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>` + c.name() + `</string><key>ProgramArguments</key><array><string>` + xmlText(c.Executable) + `</string><string>daemon</string><string>--profile</string><string>` + xmlText(c.Profile) + `</string></array><key>EnvironmentVariables</key><dict><key>PATH</key><string>` + xmlText(c.Path) + `</string></dict><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string></dict></plist>`
}

func manageService(ctx context.Context, c serviceConfig, action string, out io.Writer, command serviceCommand) error {
	call := func(name string, args ...string) ([]byte, error) { return command(ctx, name, args...) }
	if c.OS == "windows" {
		if action == "install" {
			if info, err := os.Lstat(c.launcherPath()); err == nil && !info.Mode().IsRegular() {
				return errors.New("invalid service launcher file")
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.WriteFile(c.launcherPath(), c.launcher(), 0600); err != nil {
				return err
			}
		}
		// A private script avoids command-line size limits and PowerShell's extra
		// native -Command argument parsing. It contains paths, never credentials.
		script, err := os.CreateTemp("", "runner-service-*.ps1")
		if err != nil {
			return err
		}
		defer os.Remove(script.Name())
		if err = script.Chmod(0600); err == nil {
			// Windows PowerShell 5 needs a UTF-8 BOM for non-ASCII profile paths.
			_, err = script.WriteString("\ufeff" + windowsServiceScript(c, action))
		}
		closeErr := script.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		data, err := call("powershell.exe", "-NoProfile", "-NonInteractive", "-File", script.Name())
		if err != nil {
			return err
		}
		if action == "uninstall" {
			if err := os.Remove(c.launcherPath()); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if action == "status" {
			var status struct {
				Installed bool `json:"installed"`
				Running   bool `json:"running"`
			}
			if err := json.Unmarshal(data, &status); err != nil {
				return errors.New("invalid service manager response")
			}
			return json.NewEncoder(out).Encode(status)
		}
	} else if c.OS == "linux" || c.OS == "darwin" {
		path := filepath.Join(c.Home, ".config", "systemd", "user", c.name()+".service")
		content := c.unit()
		if c.OS == "darwin" {
			path = filepath.Join(c.Home, "Library", "LaunchAgents", c.name()+".plist")
			content = c.plist()
		}
		_, statErr := os.Lstat(path)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		installed := statErr == nil
		if action == "status" {
			running := false
			if installed {
				if c.OS == "linux" {
					data, err := call("systemctl", "--user", "show", c.name()+".service", "--property=ActiveState", "--value")
					if err != nil {
						return err
					}
					running = strings.TrimSpace(string(data)) == "active"
				} else {
					data, err := call("launchctl", "print", "gui/"+c.UID+"/"+c.name())
					if err != nil {
						return err
					}
					running = strings.Contains(string(data), "state = running")
				}
			}
			return json.NewEncoder(out).Encode(map[string]bool{"installed": installed, "running": running})
		}
		if action == "install" {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			if installed {
				return errors.New("service already installed; uninstall before reinstalling")
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.WriteString(content)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if c.OS == "linux" {
				if _, err := call("systemctl", "--user", "daemon-reload"); err != nil {
					return err
				}
				if _, err := call("systemctl", "--user", "enable", "--now", c.name()+".service"); err != nil {
					return err
				}
			} else {
				if _, err := call("launchctl", "bootstrap", "gui/"+c.UID, path); err != nil {
					return err
				}
			}
		} else if installed {
			if c.OS == "linux" {
				if _, err := call("systemctl", "--user", "disable", "--now", c.name()+".service"); err != nil {
					return err
				}
			} else {
				if _, err := call("launchctl", "bootout", "gui/"+c.UID, path); err != nil {
					return err
				}
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			if c.OS == "linux" {
				if _, err := call("systemctl", "--user", "daemon-reload"); err != nil {
					return err
				}
			}
		}
	} else {
		return errors.New("background service unavailable on this operating system")
	}
	_, err := fmt.Fprintln(out, "service "+action+" completed")
	return err
}

package clientwork

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func hideHiddenProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			killer := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
			killer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			if err := killer.Run(); err != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
	}
}

// Authenticate a separate non-admin OS account and deny shared executor-profile
// access. No case byte reaches the child before identity and ACL setup succeeds.
func isolateHiddenCommand(cmd *exec.Cmd, c HiddenVerifierConfig, root string) (func() error, error) {
	fail := errors.New("verified OS isolation unavailable")
	if c.OSUser == "" || c.PasswordEnv == "" {
		return nil, fail
	}
	password := os.Getenv(c.PasswordEnv)
	if password == "" {
		return nil, fail
	}
	user, e := windows.UTF16PtrFromString(c.OSUser)
	if e != nil {
		return nil, fail
	}
	domain, e := windows.UTF16PtrFromString(c.OSDomain)
	if e != nil {
		return nil, fail
	}
	secret, e := windows.UTF16FromString(password)
	if e != nil {
		return nil, fail
	}
	defer func() {
		for i := range secret {
			secret[i] = 0
		}
	}()
	var token windows.Token
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(user)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(&secret[0])), 2, 0, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		return nil, fail
	}
	cleanup := func() error { return token.Close() }
	reject := func() (func() error, error) { _ = cleanup(); return nil, fail }
	account, e := token.GetTokenUser()
	if e != nil {
		return reject()
	}
	current, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil || windows.EqualSid(account.User.Sid, current.User.Sid) {
		return reject()
	}
	admins, e := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if e != nil {
		return reject()
	}
	groups, e := token.GetTokenGroups()
	if e != nil {
		return reject()
	}
	for _, group := range groups.AllGroups() {
		if windows.EqualSid(group.Sid, admins) {
			return reject()
		}
	}
	profile := filepath.Join(root, "profile")
	if os.MkdirAll(profile, 0700) != nil {
		return reject()
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + account.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return reject()
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		return reject()
	}
	// Set owner as well: a same-account executor must not be able to restore its
	// own access by virtue of owning the directory. Missing privilege fails shut.

	paths := []string{}
	e = filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		attrs, attrErr := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
		if attrErr != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fail
		}
		paths = append(paths, path)
		return nil
	})
	if e != nil {
		return reject()
	}
	restoreSD, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + current.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return reject()
	}
	restoreACL, _, e := restoreSD.DACL()
	if e != nil {
		return reject()
	}
	// Setup rollback occurs before any secret is delivered. Parent-first
	// restoration is used only if native isolation setup fails.
	cleanup = func() error {
		var restoreErr error
		for _, path := range paths {
			e := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION, current.User.Sid, nil, restoreACL, nil)
			if e != nil {
				restoreErr = e
			}
		}
		return errors.Join(restoreErr, token.Close())
	}
	for i := len(paths) - 1; i >= 0; i-- {
		e = windows.SetNamedSecurityInfo(paths[i], windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION, account.User.Sid, nil, dacl, nil)
		if e != nil {
			return reject()
		}
	}
	cmd.SysProcAttr.Token = syscall.Token(token)
	cmd.Env = hiddenEnvironment(os.Environ())
	filtered := cmd.Env[:0]
	for _, item := range cmd.Env {
		key, _, _ := strings.Cut(item, "=")
		if !slices.Contains([]string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"}, strings.ToUpper(key)) {
			filtered = append(filtered, item)
		}
	}
	cmd.Env = append(filtered, "HOME="+profile, "USERPROFILE="+profile, "APPDATA="+profile, "LOCALAPPDATA="+profile, "TEMP="+profile, "TMP="+profile)
	if c.RuntimeAuthEnv != "" {
		credential := os.Getenv(c.RuntimeAuthEnv)
		if credential == "" {
			return reject()
		}
		switch c.Family {
		case "openai":
			cmd.Env = append(cmd.Env, "OPENAI_API_KEY="+credential)
		case "anthropic":
			cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+credential)
		default:
			return reject()
		}
	}

	cleanup = func() error {
		done := make(chan error, 1)
		go func() {
			runtime.LockOSThread()
			impersonate := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser")
			ok, _, _ := impersonate.Call(uintptr(token))
			if ok == 0 {
				runtime.UnlockOSThread()
				done <- fail
				return
			}
			deleteErr := os.RemoveAll(root)
			reverted, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RevertToSelf").Call()
			// A failed revert discards this locked thread when the goroutine exits.
			if reverted != 0 {
				runtime.UnlockOSThread()
			} else {
				deleteErr = errors.Join(deleteErr, fail)
			}
			done <- deleteErr
		}()
		cleanupErr := <-done
		return errors.Join(cleanupErr, token.Close())
	}

	return cleanup, nil
}

// The trusted adapter gets no stdin until its process belongs to a kill-on-close
// job. Closing that job terminates all runtime descendants before private cleanup.
func runHiddenProcess(cmd *exec.Cmd, raw []byte) (terminated bool, runErr error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return true, err
	}
	defer func() { runErr = errors.Join(runErr, windows.CloseHandle(job)) }()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return true, err
	}
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return true, err
	}
	if err = cmd.Start(); err != nil {
		_ = pipe.Close()
		return true, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		_ = windows.CloseHandle(handle)
	}
	if err != nil {
		_ = pipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, err
	}
	_, writeErr := pipe.Write(raw)
	_ = pipe.Close()
	waitErr := cmd.Wait()
	// Terminate descendants even after a successful adapter exit, then wait for
	// the job to contain no active process before its private token is released.
	_ = windows.TerminateJobObject(job, 1)
	// Query uses the documented JOBOBJECT_BASIC_ACCOUNTING_INFORMATION layout.
	var accounting struct {
		TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
	}
	terminationDeadline := time.Now().Add(5 * time.Second)
	for {
		queryErr := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		if queryErr == nil && accounting.ActiveProcesses == 0 {
			break
		}
		if queryErr != nil || time.Now().After(terminationDeadline) {
			return false, errors.New("private verifier termination unconfirmed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if writeErr != nil {
		return true, writeErr
	}
	return true, waitErr
}

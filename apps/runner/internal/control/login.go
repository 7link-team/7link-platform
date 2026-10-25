package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"7link.com/runner/internal/inspection"
)

type LoginOptions struct{ Server, TenantID, ClientID, Name string }
type enrollment struct {
	ID     string `json:"id"`
	Device struct {
		Code      string `json:"device_code"`
		UserCode  string `json:"user_code"`
		URI       string `json:"verification_uri"`
		ExpiresIn int64  `json:"expires_in"`
		Interval  int    `json:"interval"`
	} `json:"device"`
}

type enrollmentFacts struct {
	Hostname string              `json:"hostname"`
	Arch     string              `json:"arch"`
	Runtimes []enrollmentRuntime `json:"runtimes"`
}
type enrollmentRuntime struct {
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

func observeEnrollment(ctx context.Context) (enrollmentFacts, error) {
	host, e := os.Hostname()
	if e != nil {
		return enrollmentFacts{}, e
	}
	probe, stop := context.WithTimeout(ctx, 40*time.Second)
	defer stop()
	report := inspection.Inspect(probe)
	if e = ctx.Err(); e != nil {
		return enrollmentFacts{}, e
	}
	facts := enrollmentFacts{Hostname: host, Arch: report.Arch, Runtimes: []enrollmentRuntime{}}
	for _, item := range report.Executables {
		if item.Kind != "CODEX" && item.Kind != "CLAUDE_CODE" {
			continue
		}
		facts.Runtimes = append(facts.Runtimes, enrollmentRuntime{Kind: item.Kind, Available: item.Available, Version: item.Version, Error: item.Error})
	}
	return facts, nil
}

func Login(ctx context.Context, store *Store, options LoginOptions, out io.Writer) error {
	return login(ctx, store, options, out, observeEnrollment)
}

func login(ctx context.Context, store *Store, options LoginOptions, out io.Writer, observe func(context.Context) (enrollmentFacts, error)) error {
	if !validID(options.TenantID) || !validID(options.ClientID) || options.Name == "" {
		return errors.New("login requires --server, --tenant, --client and --name")
	}
	if _, err := store.Profile(); err == nil {
		return errors.New("profile already enrolled; choose a new profile directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c, err := NewClient(options.Server)
	if err != nil {
		return err
	}
	osName := map[string]string{"windows": "WINDOWS", "darwin": "MACOS", "linux": "LINUX"}[runtime.GOOS]
	if osName == "" {
		return errors.New("unsupported operating system")
	}
	facts, err := observe(ctx)
	if err != nil {
		return err
	}
	var start enrollment
	err = c.JSON(ctx, "POST", "/machine-enrollments", map[string]any{"tenant_id": options.TenantID, "client_id": options.ClientID, "name": options.Name, "os": osName, "facts": facts}, &start, false)
	if err != nil {
		return err
	}
	if !validID(start.ID) || start.Device.Code == "" || start.Device.ExpiresIn < 1 || start.Device.ExpiresIn > 3600 || start.Device.Interval < 1 || start.Device.Interval > 60 {
		return errors.New("invalid enrollment response")
	}
	// A local interrupt withdraws only a still-pending request. Use a separate,
	// bounded context because the signal has already canceled the login context.
	// Never retry a credential exchange after an ambiguous response.
	defer func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
			defer stop()
			if e := c.JSON(cleanup, "POST", "/machine-enrollments/"+start.ID+"/cancellation", map[string]string{"tenant_id": options.TenantID, "device_code": start.Device.Code}, nil, false); e != nil {
				_, _ = fmt.Fprintln(out, "Enrollment cancellation was not confirmed; the pending code will expire automatically.")
			}
		}
	}()
	if _, err = fmt.Fprintf(out, "Open %s and confirm code %s.\n", start.Device.URI, start.Device.UserCode); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(start.Device.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(start.Device.Interval) * time.Second
	for {
		if err = wait(waitCtx, interval); err != nil {
			return err
		}
		var credential struct {
			MachineID  string `json:"machine_id"`
			Credential struct {
				Credential struct {
					ID string `json:"id"`
				} `json:"credential"`
				Secret string `json:"client_secret"`
			} `json:"credential"`
		}
		err = c.JSON(waitCtx, "POST", "/machine-enrollments/"+start.ID+"/credential", map[string]string{"tenant_id": options.TenantID, "device_code": start.Device.Code}, &credential, false)
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && api.Status == 409 && api.Reason == "AUTHORIZATION_PENDING" {
				continue
			}
			if errors.As(err, &api) && (api.Status == 409 || api.Status == 429) && api.Reason == "SLOW_DOWN" {
				interval += 5 * time.Second
				continue
			}
			return err
		}
		p := Profile{Server: options.Server, TenantID: options.TenantID, MachineID: credential.MachineID, ClientID: credential.Credential.Credential.ID, Secret: credential.Credential.Secret}
		if !validID(p.MachineID) || !validID(p.ClientID) || p.Secret == "" {
			return errors.New("invalid machine credential response")
		}
		// Persist before the first session acknowledges credential delivery. Never
		// print the one-time credential or use the human device token as identity.
		if err = store.SaveProfile(p); err != nil {
			return errors.New("cannot persist machine credential; restart enrollment")
		}
		if err = c.Authenticate(ctx, p); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Machine enrolled. Run daemon with the same --profile directory.")
		return err
	}
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

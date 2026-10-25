package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// RunnerInstallConfig describes an operator-published package, never an inferred release.
type RunnerInstallConfig struct {
	Package   string            `mapstructure:"package"`
	Version   string            `mapstructure:"version"`
	Command   string            `mapstructure:"command"`
	Server    string            `mapstructure:"server"`
	ClientIDs map[string]string `mapstructure:"client_ids"`
}

type EnrollmentSettings struct {
	Available bool   `json:"available"`
	Install   string `json:"install"`
	Login     string `json:"login"`
	Start     string `json:"start"`
}

type EnrollmentStatus struct {
	ID                guid.ID          `json:"id"`
	Name              string           `json:"name"`
	OS                string           `json:"os"`
	State             string           `json:"state"`
	ExpiresAt         time.Time        `json:"expires_at"`
	ServerTime        time.Time        `json:"server_time"`
	MachineID         *guid.ID         `json:"machine_id,omitempty"`
	Facts             *EnrollmentFacts `json:"facts,omitempty"`
	MachineState      string           `json:"machine_state,omitempty" enum:",ONLINE,OFFLINE,DRAINING,REVOKED"`
	ConnectionDelayed bool             `json:"connection_delayed"`
}

func (m *Module) enrollmentStatusOn(ctx context.Context, tx *gorm.DB, row *model.Enrollment) (EnrollmentStatus, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	state := row.State
	if state == "PENDING" && !row.ExpiresAt.After(now) {
		state = "EXPIRED"
	}
	result := EnrollmentStatus{ID: row.ID, Name: row.Name, OS: row.OS, State: state, ExpiresAt: row.ExpiresAt, ServerTime: now, MachineID: row.MachineID}
	if row.FactsJSON != nil {
		var facts EnrollmentFacts
		if e := json.Unmarshal([]byte(*row.FactsJSON), &facts); e != nil {
			return EnrollmentStatus{}, e
		}
		if !validateEnrollmentFacts(&facts) {
			return EnrollmentStatus{}, ErrUnavailable
		}
		result.Facts = &facts
	}
	if row.MachineID != nil {
		q := query.BindMachines(tx, m.names)
		machine, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(*row.MachineID)).First()
		if e != nil {
			return EnrollmentStatus{}, e
		}
		result.MachineState = machineState(machine, now)
		result.ConnectionDelayed = result.MachineState == "OFFLINE" && now.Sub(machine.CreatedAt) >= 10*time.Minute
	}
	return result, nil
}

var enrollmentCodePattern = regexp.MustCompile(`^[A-Z0-9]{8}$`)

// LookupEnrollment returns only a pending, unexpired enrollment matching both
// the private stored code hash and IAM's tenant/client grant. Terminal requests
// and every missing selector have the same not-found response.
func (m *Module) LookupEnrollment(ctx context.Context, tenant guid.ID, code string) (EnrollmentStatus, error) {
	var result EnrollmentStatus
	db, e := m.writer()
	if e != nil {
		return result, e
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
			return e
		}
		canonical := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
		if !enrollmentCodePattern.MatchString(canonical) {
			return ErrMachineNotFound
		}
		q := query.BindEnrollments(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.UserHash.Eq(userCodeHash(code)), q.State.Eq("PENDING"), q.ExpiresAt.Gt(time.Now().UTC())).Limit(2).Find()
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return ErrMachineNotFound
		}
		if !m.enrollmentIAM.Bound() {
			return ErrUnavailable
		}
		grant, e := m.enrollmentIAM.LookupDevice(ctx, tenant, canonical[:4]+"-"+canonical[4:])
		if e != nil {
			if errors.Is(e, oauth.ErrDeviceRequestNotFound) || errors.Is(e, oauth.ErrExpiredToken) || errors.Is(e, oauth.ErrAccessDenied) {
				return ErrMachineNotFound
			}
			return e
		}
		if grant.ClientID != rows[0].ClientID || grant.Status != "pending" || !grant.ExpiresAt.After(time.Now().UTC()) {
			return ErrMachineNotFound
		}
		result, e = m.enrollmentStatusOn(ctx, tx, rows[0])
		return e
	})
	return result, e
}

func (m *Module) EnrollmentStatus(ctx context.Context, tenant, id guid.ID) (EnrollmentStatus, error) {
	var result EnrollmentStatus
	db, err := m.writer()
	if err != nil {
		return result, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
			return e
		}
		row, e := m.enrollmentOn(ctx, tx, tenant, id)
		if e != nil {
			return e
		}
		result, e = m.enrollmentStatusOn(ctx, tx, row)
		return e
	})
	return result, err
}

var runnerPackagePattern = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var runnerVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)
var runnerCommandPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func (m *Module) EnrollmentSettings(ctx context.Context, tenant guid.ID, os string) (EnrollmentSettings, error) {
	var result EnrollmentSettings
	if os != "WINDOWS" && os != "MACOS" && os != "LINUX" {
		return result, ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return result, err
	}
	if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.controlAuthorityOn(ctx, tx, tenant, "machine_manage") }); err != nil {
		return result, err
	}
	cfg := m.config.Runner
	client, err := guid.RequireID(cfg.ClientIDs[tenant.String()])
	server, parseErr := url.Parse(cfg.Server)
	if err != nil || parseErr != nil || server.Host == "" || server.User != nil || server.RawQuery != "" || server.Fragment != "" || (server.Scheme != "https" && server.Scheme != "http") || !runnerPackagePattern.MatchString(cfg.Package) || !runnerVersionPattern.MatchString(cfg.Version) || !runnerCommandPattern.MatchString(cfg.Command) {
		return result, nil //nolint:nilerr // Invalid deployment configuration makes installation explicitly unavailable.
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	if os == "WINDOWS" {
		quote = func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	}
	result.Available = true
	result.Install = "npm install -g " + quote(cfg.Package+"@"+cfg.Version)
	// CLI requires explicit profile and display name. Keep placeholders visible and editable.
	result.Login = cfg.Command + " login --profile " + quote("./machine-profile") + " --server " + quote(cfg.Server) + " --tenant " + tenant.String() + " --client " + client.String() + " --name " + quote("My machine")
	result.Start = cfg.Command + " daemon --profile " + quote("./machine-profile")
	return result, nil
}

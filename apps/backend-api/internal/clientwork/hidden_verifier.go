package clientwork

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// HiddenVerifierConfig is operator configuration, never an executor request.
// Executable is a trusted verifier adapter, outside the repository under test.
// It reads HiddenVerifierInput on stdin and writes one HiddenVerifierOutput JSON.
type HiddenVerifierConfig struct {
	PrincipalID                                   guid.ID
	Executable                                    string
	Args                                          []string
	Model, Family                                 string
	TimeoutSec                                    int
	OSUser, OSDomain, PasswordEnv, RuntimeAuthEnv string
}
type HiddenVerifierInput struct {
	Nonce      string            `json:"nonce"`
	SuiteHash  string            `json:"suite_hash"`
	HeadCommit string            `json:"head_commit"`
	Model      string            `json:"model"`
	Family     string            `json:"family"`
	Cases      []json.RawMessage `json:"cases"`
}
type HiddenVerifierOutput struct {
	Nonce      string `json:"nonce"`
	SuiteHash  string `json:"suite_hash"`
	HeadCommit string `json:"head_commit"`
	Model      string `json:"model"`
	Family     string `json:"family"`
	Verdict    string `json:"verdict"`
}

func (m *Module) injectHiddenAcceptance(s *module.Services) error {
	var err error
	if m.hiddenService, err = module.Resolve[identity.ServiceAccountValidator](s); err != nil {
		return err
	}
	if m.hiddenPermission, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.hiddenSource, err = module.Resolve[execution.HiddenSourceFacts](s); err != nil {
		return err
	}
	connector, err := module.Resolve[delivery.HiddenAcceptanceConnector](s)
	if err != nil {
		return err
	}
	m.hiddenRequest = connector.ReadOn
	if err = connector.Bind(delivery.HiddenAcceptanceVerifier{PrincipalID: func() guid.ID { return m.config.HiddenVerifier.PrincipalID }, DispatchOn: m.dispatchHiddenOn, PassedOn: m.hiddenPassedOn, VerdictOn: m.hiddenVerdictOn}); err != nil {
		return err
	}
	automation, err := module.Resolve[delivery.VerificationAutomation](s)
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: "clientwork.hidden_verification_reconcile", EventTypes: []string{"clientwork.hidden_verified"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: func(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
		var data struct {
			WorkItemID guid.ID `json:"work_item_id"`
			ResultID   guid.ID `json:"result_id"`
		}
		if event.TenantID <= 0 || event.ProjectID <= 0 || event.Subject.Kind != "clientwork" || json.Unmarshal(event.Data, &data) != nil || data.WorkItemID <= 0 || data.ResultID <= 0 {
			return ErrInvalid
		}
		if automation.CollectOn == nil {
			return ErrUnavailable
		}
		err := automation.CollectOn(ctx, tx, event.TenantID, event.ProjectID, data.WorkItemID, data.ResultID)
		if errors.Is(err, delivery.ErrHiddenAcceptanceUnavailable) {
			return nil
		}
		return err
	}})
}
func hiddenHash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func (m *Module) archiveHiddenCaseOn(ctx context.Context, tx *gorm.DB, row *model.TestCase) error {
	raw, err := hiddenCaseContent(row)
	if err != nil {
		return err
	}
	hash := hiddenHash(raw)
	q := query.BindHiddenCaseArchive(tx.Session(&gorm.Session{Logger: logger.Discard}), m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ContentHash.Eq(hash)).First()
	if err == nil {
		if !bytes.Equal(prior.Content, raw) || prior.CaseID != row.ID || prior.CaseVersion != row.Version {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return q.WithContext(ctx).Create(&model.HiddenCaseArchive{TenantID: row.TenantID, ProjectID: row.ProjectID, ContentHash: hash, CaseID: row.ID, CaseVersion: row.Version, Content: raw, CreatedAt: now()})
}
func (m *Module) hiddenServiceAllowedOn(ctx context.Context, tx *gorm.DB, in delivery.HiddenAcceptanceRequest) error {
	c := m.config.HiddenVerifier
	if c.PrincipalID <= 0 || m.hiddenService == nil || m.hiddenPermission == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, c.PrincipalID); err != nil {
		return err
	}
	active, err := m.hiddenService(ctx, tx, in.TenantID, c.PrincipalID, now())
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	constraint, err := m.hiddenPermission(ctx, tx, in.TenantID, in.ProjectID, "acceptance_suite_verify", c.PrincipalID)
	if err != nil {
		return err
	}
	if slices.Contains(constraint.DeniedIDs, in.ProjectID.String()) || (!constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, in.ProjectID.String())) {
		return ErrForbidden
	}
	return nil
}
func (m *Module) hiddenReceiptOn(ctx context.Context, tx *gorm.DB, in delivery.HiddenAcceptanceRequest) (*model.HiddenVerificationReceipt, error) {
	q := query.BindHiddenVerificationReceipt(tx, m.names)
	return q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.WorkItemID.Eq(in.WorkItemID), q.ResultID.Eq(in.ResultID), q.SuiteHash.Eq(in.SuiteHash), q.HeadCommit.Eq(in.HeadCommit)).First()
}
func (m *Module) hiddenPassedOn(ctx context.Context, tx *gorm.DB, in delivery.HiddenAcceptanceRequest) (bool, error) {
	if err := m.hiddenServiceAllowedOn(ctx, tx, in); err != nil {
		return false, err
	}
	receipt, err := m.hiddenReceiptOn(ctx, tx, in)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expected := receipt.ReceiptHash
	unsigned := *receipt
	unsigned.ReceiptHash = ""
	raw, _ := json.Marshal(unsigned)
	return receipt.VerifierPrincipalID == m.config.HiddenVerifier.PrincipalID && receipt.Verdict == "PASSED" && expected == hiddenHash(raw), nil
}

func (m *Module) hiddenVerdictOn(ctx context.Context, tx *gorm.DB, in delivery.HiddenAcceptanceRequest) (string, error) {
	if err := m.hiddenServiceAllowedOn(ctx, tx, in); err != nil {
		return "", err
	}
	receipt, err := m.hiddenReceiptOn(ctx, tx, in)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "UNAVAILABLE", nil
	}
	if err != nil {
		return "", err
	}
	unsigned := *receipt
	unsigned.ReceiptHash = ""
	raw, _ := json.Marshal(unsigned)
	if receipt.VerifierPrincipalID != m.config.HiddenVerifier.PrincipalID || receipt.ReceiptHash != hiddenHash(raw) {
		return "UNAVAILABLE", nil
	}
	return receipt.Verdict, nil
}

const hiddenVerificationJob = "clientwork.hidden_verification"

func (m *Module) dispatchHiddenOn(ctx context.Context, tx *gorm.DB, in delivery.HiddenAcceptanceRequest) error {
	if in.ResultID <= 0 || in.ExecutionID <= 0 {
		return nil
	}
	if m.forecastQueue == nil {
		return ErrUnavailable
	}
	// Queue identifiers only. The job resolves the authoritative frozen suite;
	// neither mutable cases nor content-bearing output enter the job system.
	_, err := m.forecastQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: in.TenantID, Kind: hiddenVerificationJob, Payload: delivery.HiddenAcceptanceRequest{ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, ResultID: in.ResultID}, IdempotencyKey: "hidden:" + in.ResultID.String() + ":" + in.SuiteHash, ConcurrencyKey: "hidden:" + in.ResultID.String()})
	return err
}

func (m *Module) hiddenVerifierHandler() jobs.Handler {
	return jobs.Handler{Kind: hiddenVerificationJob, Timeout: time.Hour, Run: func(ctx context.Context, job jobs.Job) error {
		var in delivery.HiddenAcceptanceRequest
		if job.TenantID <= 0 || json.Unmarshal(job.Payload, &in) != nil || in.ProjectID <= 0 || in.WorkItemID <= 0 || in.ResultID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		in.TenantID = job.TenantID
		return m.executeHidden(ctx, in)
	}}
}

func (m *Module) executeHidden(ctx context.Context, ref delivery.HiddenAcceptanceRequest) error {
	if m.writer() == nil || m.hiddenRequest == nil {
		return ErrUnavailable
	}
	var in delivery.HiddenAcceptanceRequest
	var source execution.HiddenSource
	var input HiddenVerifierInput
	var receipt *model.HiddenVerificationReceipt
	err := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if err = m.hiddenServiceAllowedOn(ctx, tx, ref); err != nil {
			return err
		}
		in, err = m.hiddenRequest(ctx, tx, ref)
		if err != nil {
			return err
		}
		if len(in.Cases) == 0 {
			return nil
		}
		if err = m.hiddenServiceAllowedOn(ctx, tx, in); err != nil {
			return err
		}
		if _, err = m.hiddenReceiptOn(ctx, tx, in); err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if m.hiddenSource.ReadOn == nil {
			return ErrUnavailable
		}
		source, err = m.hiddenSource.ReadOn(ctx, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.ExecutionID)
		if err != nil {
			return err
		}
		id, err := m.nextID()
		if err != nil {
			return err
		}
		c := m.config.HiddenVerifier
		receipt = &model.HiddenVerificationReceipt{TenantID: in.TenantID, ID: id, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, ResultID: in.ResultID, SuiteHash: in.SuiteHash, HeadCommit: in.HeadCommit, VerifierPrincipalID: c.PrincipalID, Model: c.Model, Family: c.Family, Verdict: "UNAVAILABLE", Reason: "VERIFIER_UNAVAILABLE"}
		if source.PrincipalID == c.PrincipalID || c.Family == "" || c.Model == "" || strings.EqualFold(source.Family, c.Family) || source.Model == c.Model {
			receipt.Reason = "DIFFERENT_MODEL_FAMILY_UNAVAILABLE"
			return nil
		}
		input = HiddenVerifierInput{SuiteHash: in.SuiteHash, HeadCommit: in.HeadCommit, Model: c.Model, Family: c.Family, Cases: []json.RawMessage{}}
		nonce := make([]byte, 32)
		if _, err = rand.Read(nonce); err != nil {
			return err
		}
		input.Nonce = hex.EncodeToString(nonce)
		q := query.BindHiddenCaseArchive(tx.Session(&gorm.Session{Logger: logger.Discard}), m.names)
		for _, item := range in.Cases {
			r, e := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.ContentHash.Eq(item.ContentHash), q.CaseID.Eq(item.ID), q.CaseVersion.Eq(item.Version)).First()
			if e != nil || hiddenHash(r.Content) != item.ContentHash {
				return ErrConflict
			}
			var frozen TestCaseView
			if json.Unmarshal(r.Content, &frozen) != nil || !frozen.IsHiddenAcceptance || frozen.WorkItemID != in.WorkItemID {
				return ErrConflict
			}
			input.Cases = append(input.Cases, json.RawMessage(r.Content))
		}
		return nil
	})
	if err != nil || receipt == nil {
		return err
	}
	if len(input.Cases) > 0 {
		raw, _ := json.Marshal(input)
		receipt.InputHash = hiddenHash(raw)
		receipt.Verdict, receipt.Reason, receipt.OutputHash = m.runHiddenCommand(ctx, m.config.HiddenVerifier, source.RepositoryPath, input, raw)
	}
	// No database transaction or IAM/project lock spans the native command.
	return m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.hiddenServiceAllowedOn(ctx, tx, ref); err != nil {
			return err
		}
		latest, err := m.hiddenRequest(ctx, tx, ref)
		if err != nil {
			return err
		}
		if latest.SuiteHash != in.SuiteHash || latest.HeadCommit != in.HeadCommit || latest.ExecutionID != in.ExecutionID {
			return ErrConflict
		}
		if err = m.hiddenServiceAllowedOn(ctx, tx, in); err != nil {
			return err
		}
		if _, err = m.hiddenReceiptOn(ctx, tx, in); err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		receipt.CreatedAt = now()
		raw, _ := json.Marshal(receipt)
		receipt.ReceiptHash = hiddenHash(raw)
		if err = query.BindHiddenVerificationReceipt(tx, m.names).WithContext(ctx).Create(receipt); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, in.TenantID, "clientwork.hidden_verified", "work_item", in.WorkItemID)
		event.PrincipalID = receipt.VerifierPrincipalID
		event.Detail["result_id"], event.Detail["suite_hash"], event.Detail["receipt_hash"], event.Detail["verdict"], event.Detail["verifier_principal_id"] = in.ResultID.String(), in.SuiteHash, receipt.ReceiptHash, receipt.Verdict, receipt.VerifierPrincipalID.String()
		if m.audit == nil || m.events == nil {
			return ErrUnavailable
		}
		if err = m.audit(ctx, tx, event); err != nil {
			return err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "clientwork.hidden_verified", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "service:" + receipt.VerifierPrincipalID.String() + ":acceptance_suite_verify"}, Subject: events.Subject{Kind: "clientwork", ID: receipt.ID}, SubjectVersion: 1, Data: map[string]any{"work_item_id": in.WorkItemID.String(), "result_id": in.ResultID.String(), "suite_hash": in.SuiteHash, "receipt_hash": receipt.ReceiptHash, "verdict": receipt.Verdict}})
		return err
	})
}

// Bounded command output never reaches logs, evidence, contexts or errors. Even
// a malicious adapter echoing all input can publish only its SHA256 digest.
type hiddenOutput struct {
	data     []byte
	overflow bool
}

func (b *hiddenOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data)+n > 65536 {
		b.overflow = true
		return n, nil
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (m *Module) runHiddenCommand(ctx context.Context, c HiddenVerifierConfig, repository string, input HiddenVerifierInput, raw []byte) (verdict, reason, outputHash string) {
	if !filepath.IsAbs(repository) || !filepath.IsAbs(c.Executable) || c.TimeoutSec < 1 || c.TimeoutSec > 3600 {
		return "UNAVAILABLE", "VERIFIER_CONFIGURATION_INVALID", ""
	}
	executable, err := filepath.EvalSymlinks(c.Executable)
	if err != nil {
		return "UNAVAILABLE", "VERIFIER_COMMAND_UNAVAILABLE", ""
	}
	root, err := os.MkdirTemp("", "private-acceptance-")
	if err != nil {
		return "UNAVAILABLE", "PRIVATE_WORKSPACE_UNAVAILABLE", ""
	}
	removeRoot := true
	defer func() {
		if removeRoot {
			if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
				verdict, reason = "UNAVAILABLE", "PRIVATE_WORKSPACE_CLEANUP_FAILED"
			}
		}
	}()
	if os.Chmod(root, 0700) != nil {
		return "UNAVAILABLE", "PRIVATE_WORKSPACE_UNAVAILABLE", ""
	}
	deadline, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSec)*time.Second)
	defer cancel()
	// No local/shared clone optimization, hardlinks, repository hooks or executor
	// working tree files are reused. The verifier receives committed bytes only.
	clone := filepath.Join(root, "source")
	git := exec.CommandContext(deadline, "git", "-c", "core.hooksPath=", "-c", "core.autocrlf=false", "clone", "--no-local", "--no-checkout", "--", repository, clone)
	hideHiddenProcess(git)
	git.Env = hiddenEnvironment(os.Environ())
	git.Stdout, git.Stderr = io.Discard, io.Discard
	if git.Run() != nil {
		return "UNAVAILABLE", "SOURCE_CLONE_UNAVAILABLE", ""
	}
	checkout := exec.CommandContext(deadline, "git", "-c", "core.hooksPath=", "-c", "core.autocrlf=false", "-C", clone, "checkout", "--detach", input.HeadCommit)
	hideHiddenProcess(checkout)
	checkout.Env = hiddenEnvironment(os.Environ())
	checkout.Stdout, checkout.Stderr = io.Discard, io.Discard
	if checkout.Run() != nil {
		return "UNAVAILABLE", "FROZEN_HEAD_UNAVAILABLE", ""
	}
	if hiddenWithin(clone, executable) || hiddenWithin(repository, executable) {
		return "UNAVAILABLE", "UNTRUSTED_VERIFIER_COMMAND", ""
	}
	var output hiddenOutput
	cmd := exec.CommandContext(deadline, executable, c.Args...)
	cmd.WaitDelay = 2 * time.Second
	hideHiddenProcess(cmd)
	isolate := m.hiddenIsolation
	if isolate == nil {
		isolate = isolateHiddenCommand
	}
	cleanup, err := isolate(cmd, c, root)
	if err != nil {
		return "UNAVAILABLE", "OS_ISOLATION_UNAVAILABLE", ""
	}
	// The authenticated cleanup callback exclusively owns the secret-bearing tree.
	removeRoot = false
	cleanupAllowed := true
	defer func() {
		if cleanupAllowed {
			if cleanupErr := cleanup(); cleanupErr != nil {
				verdict, reason = "UNAVAILABLE", "PRIVATE_WORKSPACE_CLEANUP_FAILED"
			}
		}
	}()
	cmd.Dir = clone
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	terminated, commandErr := runHiddenProcess(cmd, raw)
	if !terminated {
		cleanupAllowed = false
		removeRoot = false
		return "UNAVAILABLE", "VERIFIER_TERMINATION_UNCONFIRMED", hiddenHash(output.data)
	}
	if commandErr != nil {
		return "INCONCLUSIVE", "VERIFIER_COMMAND_FAILED", hiddenHash(output.data)
	}
	if output.overflow {
		return "INCONCLUSIVE", "INVALID_VERIFIER_RECEIPT", hiddenHash(output.data)
	}
	var result HiddenVerifierOutput
	decoder := json.NewDecoder(bytes.NewReader(output.data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Nonce != input.Nonce || result.SuiteHash != input.SuiteHash || result.HeadCommit != input.HeadCommit || result.Model != c.Model || result.Family != c.Family {
		return "INCONCLUSIVE", "INVALID_VERIFIER_RECEIPT", hiddenHash(output.data)
	}
	if result.Verdict != "PASSED" && result.Verdict != "FAILED" && result.Verdict != "INCONCLUSIVE" && result.Verdict != "UNAVAILABLE" {
		return "INCONCLUSIVE", "INVALID_VERIFIER_RECEIPT", hiddenHash(output.data)
	}
	return result.Verdict, "VERIFIER_COMMAND_COMPLETED", hiddenHash(output.data)
}

func hiddenEnvironment(parent []string) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "LANG": true, "LC_ALL": true, "PATHEXT": true}
	out := []string{}
	for _, value := range parent {
		key, _, ok := strings.Cut(value, "=")
		if ok && allowed[strings.ToUpper(key)] {
			out = append(out, value)
		}
	}
	return out
}

func hiddenWithin(root, path string) bool {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(resolved, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

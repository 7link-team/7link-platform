package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestDefectFactsRequireExactMachineSignedContainment(t *testing.T) {
	for _, scenario := range []string{"trusted", "unsigned", "wrong_signature", "wrong_commit", "wrong_project", "wrong_work"} {
		t.Run(scenario, func(t *testing.T) {
			f, id, head := mergeFixture(t)
			var request MergeView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				request, err = f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Verify exact artifact containment"})
				return err
			}))
			var plans []MergePlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				plans, err = f.m.pullMergeOn(f.runner, tx, 7, f.fence)
				return err
			}))
			require.Len(t, plans, 1)
			plan := plans[0]
			raw, err := json.Marshal(plan)
			require.NoError(t, err)
			now := time.Now().UTC()
			o := MergeObservation{Contained: true, PlanHash: hash(raw), State: "MERGED", SourceCommit: head, MergeCommit: strings.Repeat("c", 40), Reason: "MERGE_COMMITTED", StartedAt: now, EndedAt: now}
			raw, err = json.Marshal(o)
			require.NoError(t, err)
			mac := hmac.New(sha256.New, []byte(plan.Nonce))
			_, err = mac.Write(raw)
			require.NoError(t, err)
			o.Signature = hex.EncodeToString(mac.Sum(nil))
			if scenario == "unsigned" {
				o.Contained = false
				o.Signature = ""
			}
			if scenario == "wrong_signature" {
				o.Signature = strings.Repeat("0", 64)
			}
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.completeMergeOn(f.runner, tx, 7, request.ID, f.fence, plan.Nonce, o)
			})
			if scenario == "wrong_signature" {
				require.ErrorIs(t, err, ErrFenced)
				return
			}
			require.NoError(t, err)
			row, err := f.m.rowOnForMergeTest(f, id)
			require.NoError(t, err)
			project, work := row.ProjectID, row.WorkItemID
			if scenario == "wrong_commit" {
				head = strings.Repeat("f", 40)
			}
			if scenario == "wrong_project" {
				project++
			}
			if scenario == "wrong_work" {
				work++
			}
			var fact delivery.DefectMergeFact
			err = f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				fact, e = f.m.defectMergeOn(f.human, tx, guid.ID(7), project, work, id, head)
				return e
			})
			if scenario == "trusted" {
				require.NoError(t, err)
				require.Equal(t, head, fact.SourceCommit)
				require.Equal(t, request.ID, fact.MergeID)
				require.Len(t, fact.ReceiptHash, 64)
			} else {
				require.Error(t, err)
			}
		})
	}
}

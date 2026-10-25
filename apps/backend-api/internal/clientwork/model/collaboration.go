package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type ChangeRequest struct {
	Proposal        *string   `gorm:"column:proposal"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	SheetID         guid.ID   `gorm:"column:sheet_id;not null"`
	BaseRevision    int       `gorm:"column:base_revision;not null"`
	Description     string    `gorm:"column:description;not null"`
	State           string    `gorm:"column:state;not null;size:32"`
	Impact          string    `gorm:"column:impact;not null"`
	ProposalHash    string    `gorm:"column:proposal_hash;not null;size:64"`
	AppliedChangeID *guid.ID  `gorm:"column:applied_change_id"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	Version         int       `gorm:"column:version;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ChangeRequest) TableName() string { return "clientwork_change_requests" }

type ClientIssue struct {
	TenantID         guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID        guid.ID   `gorm:"column:project_id;not null"`
	Title            string    `gorm:"column:title;not null;size:200"`
	Description      string    `gorm:"column:description;not null"`
	State            string    `gorm:"column:state;not null;size:32"`
	ArtifactID       *guid.ID  `gorm:"column:artifact_id"`
	Anchor           string    `gorm:"column:anchor;not null"`
	InternalDefectID *guid.ID  `gorm:"column:internal_defect_id"`
	ActorID          guid.ID   `gorm:"column:actor_id;not null"`
	Version          int       `gorm:"column:version;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (ClientIssue) TableName() string { return "clientwork_issues" }

type IssueReply struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID guid.ID   `gorm:"column:project_id;not null"`
	IssueID   guid.ID   `gorm:"column:issue_id;not null"`
	Body      string    `gorm:"column:body;not null"`
	ActorID   guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (IssueReply) TableName() string { return "clientwork_issue_replies" }

type DeliveryPackage struct {
	ArchivedAt   *time.Time `gorm:"column:archived_at"`
	TenantID     guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID           guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID    guid.ID    `gorm:"column:project_id;not null"`
	Title        string     `gorm:"column:title;not null;size:200"`
	ReleaseNotes string     `gorm:"column:release_notes;not null"`
	Manifest     string     `gorm:"column:manifest;not null"`
	ContentHash  string     `gorm:"column:content_hash;not null;size:64"`
	State        string     `gorm:"column:state;not null;size:32"`
	RecipientID  guid.ID    `gorm:"column:recipient_id;not null"`
	ActorID      guid.ID    `gorm:"column:actor_id;not null"`
	Version      int        `gorm:"column:version;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (DeliveryPackage) TableName() string { return "clientwork_packages" }

type PackageReceipt struct {
	Source            string    `gorm:"column:source;not null;default:PORTAL"`
	RecordedBy        *guid.ID  `gorm:"column:recorded_by"`
	EvidenceBindingID *guid.ID  `gorm:"column:evidence_binding_id"`
	EvidenceJSON      *string   `gorm:"column:evidence_json"`
	SignerName        *string   `gorm:"column:signer_name;size:200"`
	SignerContactID   *guid.ID  `gorm:"column:signer_contact_id"`
	ReceiptSHA256     *string   `gorm:"column:receipt_sha256;size:64"`
	ReceiptJSON       *string   `gorm:"column:receipt_json"`
	ItemOutcomes      *string   `gorm:"column:item_outcomes"`
	TenantID          guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID         guid.ID   `gorm:"column:project_id;not null"`
	PackageID         guid.ID   `gorm:"column:package_id;not null"`
	ContentHash       string    `gorm:"column:content_hash;not null;size:64"`
	Decision          string    `gorm:"column:decision;not null;size:32"`
	Reason            string    `gorm:"column:reason;not null"`
	ActorID           guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (PackageReceipt) TableName() string { return "clientwork_package_receipts" }

type Preview struct {
	SourceFileID  *guid.ID  `gorm:"column:source_file_id"`
	SourceStage   string    `gorm:"column:source_stage;not null;size:16"`
	BindingID     *guid.ID  `gorm:"column:binding_id"`
	TenantID      guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	Title         string    `gorm:"column:title;not null;size:200"`
	WorkItemID    guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID      guid.ID   `gorm:"column:result_id;not null"`
	EvidenceID    guid.ID   `gorm:"column:evidence_id;not null"`
	FileVersionID guid.ID   `gorm:"column:file_version_id;not null"`
	ContentHash   string    `gorm:"column:content_hash;not null;size:64"`
	ClientVisible bool      `gorm:"column:client_visible;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (Preview) TableName() string { return "clientwork_previews" }

type Annotation struct {
	TenantID      guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	PreviewID     guid.ID   `gorm:"column:preview_id;not null"`
	FileVersionID guid.ID   `gorm:"column:file_version_id;not null"`
	ContentHash   string    `gorm:"column:content_hash;not null;size:64"`
	Anchor        string    `gorm:"column:anchor;not null"`
	Body          string    `gorm:"column:body;not null"`
	ClientVisible bool      `gorm:"column:client_visible;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (Annotation) TableName() string { return "clientwork_annotations" }

type TestCase struct {
	RequirementID      *guid.ID  `gorm:"column:requirement_id"`
	CriterionID        *guid.ID  `gorm:"column:criterion_id"`
	CriterionSource    string    `gorm:"column:criterion_source;not null;default:'';size:2000"`
	Preconditions      string    `gorm:"column:preconditions;not null;default:'';size:8000"`
	Priority           string    `gorm:"column:priority;not null;default:P2;size:2"`
	Unclassified       bool      `gorm:"column:unclassified;not null;default:false"`
	IsHiddenAcceptance bool      `gorm:"column:is_hidden_acceptance;not null;default:false"`
	TenantID           guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                 guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID          guid.ID   `gorm:"column:project_id;not null"`
	Title              string    `gorm:"column:title;not null;size:200"`
	Steps              string    `gorm:"column:steps;not null"`
	Expected           string    `gorm:"column:expected;not null"`
	WorkItemID         guid.ID   `gorm:"column:work_item_id;not null"`
	DefectID           *guid.ID  `gorm:"column:defect_id"`
	Version            int       `gorm:"column:version;not null"`
	ActorID            guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (TestCase) TableName() string { return "clientwork_test_cases" }

type TestPlan struct {
	TenantID  guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID guid.ID   `gorm:"column:project_id;not null"`
	Title     string    `gorm:"column:title;not null;size:200"`
	Cases     string    `gorm:"column:cases;not null"`
	State     string    `gorm:"column:state;not null;size:32"`
	Version   int       `gorm:"column:version;not null"`
	ActorID   guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (TestPlan) TableName() string { return "clientwork_test_plans" }

type TestRun struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	PlanID      guid.ID   `gorm:"column:plan_id;not null"`
	CaseID      guid.ID   `gorm:"column:case_id;not null"`
	CaseVersion int       `gorm:"column:case_version;not null"`
	Result      string    `gorm:"column:result;not null;size:32"`
	Evidence    string    `gorm:"column:evidence;not null"`
	DefectID    *guid.ID  `gorm:"column:defect_id"`
	ActorID     guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (TestRun) TableName() string { return "clientwork_test_runs" }

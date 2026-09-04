package query

import (
	"7link.com/platform/internal/iam/provisioning/model"
	"gorm.io/gorm"
)

type (
	Directories     = directory
	Credentials     = credential
	Resources       = resource
	Targets         = target
	TargetResources = targetResource
)

type Tables struct {
	Directories     *Directories
	Credentials     *Credentials
	Resources       *Resources
	Targets         *Targets
	TargetResources *TargetResources
}

func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Directories:     q.Directory.Table(prefix + model.Directory{}.TableName()),
		Credentials:     q.Credential.Table(prefix + model.Credential{}.TableName()),
		Resources:       q.Resource.Table(prefix + model.Resource{}.TableName()),
		Targets:         q.Target.Table(prefix + model.Target{}.TableName()),
		TargetResources: q.TargetResource.Table(prefix + model.TargetResource{}.TableName()),
	}
}

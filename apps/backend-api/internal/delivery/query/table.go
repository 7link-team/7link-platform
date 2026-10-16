package query

import (
	"7link.com/platform/internal/delivery/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// TableNames holds the names resolved once for this deployment.
type TableNames struct{ Projects, Contacts, Repositories, Counters, Requirements, Criteria, RequirementVersions gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Counters: gormx.NewTableName(prefix, model.ProjectCounter{}.TableName()), Requirements: gormx.NewTableName(prefix, model.Requirement{}.TableName()), Criteria: gormx.NewTableName(prefix, model.AcceptanceCriterion{}.TableName()), RequirementVersions: gormx.NewTableName(prefix, model.RequirementVersion{}.TableName()), Projects: gormx.NewTableName(prefix, model.Project{}.TableName()), Contacts: gormx.NewTableName(prefix, model.ProjectContact{}.TableName()), Repositories: gormx.NewTableName(prefix, model.ProjectRepository{}.TableName())}
}
func BindProjects(db *gorm.DB, names TableNames) *project {
	return Use(db).Project.Table(names.Projects.String())
}
func BindContacts(db *gorm.DB, names TableNames) *projectContact {
	return Use(db).ProjectContact.Table(names.Contacts.String())
}
func BindRepositories(db *gorm.DB, names TableNames) *projectRepository {
	return Use(db).ProjectRepository.Table(names.Repositories.String())
}

func BindCounters(db *gorm.DB, names TableNames) *projectCounter {
	return Use(db).ProjectCounter.Table(names.Counters.String())
}
func BindRequirements(db *gorm.DB, names TableNames) *requirement {
	return Use(db).Requirement.Table(names.Requirements.String())
}
func BindCriteria(db *gorm.DB, names TableNames) *acceptanceCriterion {
	return Use(db).AcceptanceCriterion.Table(names.Criteria.String())
}
func BindRequirementVersions(db *gorm.DB, names TableNames) *requirementVersion {
	return Use(db).RequirementVersion.Table(names.RequirementVersions.String())
}

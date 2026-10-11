package authn

import (
	"context"

	"7link.com/platform/pkg/guid"
	"gorm.io/gorm"
)

// AccountViewAuthorizer verifies current operator authority and target admission
// using the transaction that already holds the actor and target principal locks.
// A zero target denotes an organization view; scope and ID identify its object.
type AccountViewAuthorizer func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) error

// AccountViewAuthorizerConnector binds the authority owner to token verification.
type AccountViewAuthorizerConnector struct {
	Bind func(AccountViewAuthorizer) error
}

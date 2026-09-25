package files

// The file tree keeps two vocabularies that happen to spell some of their
// values the same way, which is why they are written out here rather than
// left as literals at the sixty-odd places that compare them. A bare
// "department" reads identically whether it names what a root belongs to or
// what kind of subject an ACL rule grants to, and the compiler cannot tell
// them apart; naming them does.

// spaceKind names how a file space is keyed. A space belongs either to one
// person or to one tenant. The roots inside a tenant's space may still belong
// to the tenant itself, to an entity or to a department -- that is the owner
// scope below, not the space.
const (
	spaceAccount = "account"
	spaceTenant  = "tenant"
)

// ownerScope names what a file root belongs to. A browsable collection uses
// the same vocabulary, because a collection lists the roots of one owner.
//
// A person owns one file space and it is ownerAccount. There was a second one,
// a "personal" root carved out of each tenant's space, and it existed because
// a caller with a tenant selected could not reach their own space at all. Now
// that they can, one person has one space again.
const (
	ownerAccount    = "account"
	ownerTenant     = "tenant"
	ownerEntity     = "entity"
	ownerDepartment = "department"
)

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
// ownerPersonal is a second personal area held inside a tenant's space, and
// nothing reaches it: the HTTP entry point that creates roots only ever asks
// for ownerAccount, and no collection lists it. It survives here so the value
// still has a name while the removal is decided.
const (
	ownerAccount    = "account"
	ownerPersonal   = "personal"
	ownerTenant     = "tenant"
	ownerEntity     = "entity"
	ownerDepartment = "department"
)

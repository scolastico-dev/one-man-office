package api

// ProfileInUse is the Details of the in_use error of DELETE
// /api/profiles/{name} while roles list models of the profile: the roles,
// in the order of the roles page. Deleting with ?detach=true takes the
// models out of them.
type ProfileInUse struct {
	Roles []string `json:"roles"`
}

// ProfileDeleted is the answer of DELETE /api/profiles/{name}?detach=true.
type ProfileDeleted struct {
	// Detached lists the roles whose models lost the profile's models, in
	// the order of the roles page; empty when no role listed one.
	Detached []DetachedRole `json:"detached"`
}

// DetachedRole is one role a profile deletion changed.
type DetachedRole struct {
	Role string `json:"role"`
	// Models are the model ids taken out of the role, in its order.
	Models []string `json:"models"`
}

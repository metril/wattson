package store

import "errors"

var (
	// ErrNotFound is returned when a row addressed by id does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrNameConflict reports a UNIQUE constraint violation on a name column.
	ErrNameConflict = errors.New("store: name already exists")
	// ErrAlreadySetup is returned when first-run setup is attempted after an admin exists.
	ErrAlreadySetup = errors.New("store: already set up")
	// ErrLastAdmin is returned when an operation would remove the last enabled admin.
	ErrLastAdmin = errors.New("store: cannot remove the last admin")
	// ErrBootstrap is returned when an operation is not allowed on the bootstrap user.
	ErrBootstrap = errors.New("store: not allowed on bootstrap user")
	// ErrAmbiguous is returned when a lookup that must identify one row matches several.
	ErrAmbiguous = errors.New("store: ambiguous match")
	// ErrIssSubMismatch is returned when an OIDC identity has an issuer without a subject or vice versa.
	ErrIssSubMismatch = errors.New("store: iss and sub must be set together")
)

// ErrLastRider is returned when deleting a user's only rider.
var ErrLastRider = errors.New("store: cannot delete the last rider")

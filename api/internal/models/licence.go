package models

import (
	"fmt"
	"slices"

	"loomtale/api/internal/pipeline"
)

// LicenceAllowlist is every SPDX licence a model may be installed or
// loaded under: permissive licences that allow commercial use of the
// model and its outputs. Anything else is refused, including licences
// that are merely "open" with use restrictions (e.g. the RAIL family)
// and every non-commercial licence.
var LicenceAllowlist = []string{"Apache-2.0", "MIT", "BSD-2-Clause", "BSD-3-Clause"}

// LicenceAllowed reports whether spdx is on the allowlist.
func LicenceAllowed(spdx string) bool {
	return slices.Contains(LicenceAllowlist, spdx)
}

// ErrLicenceRefused wraps pipeline.ErrLicenceRefused so a step that hits
// the gate fails permanently instead of retrying.
var ErrLicenceRefused = fmt.Errorf("models: %w", pipeline.ErrLicenceRefused)

// Gate refuses e unless its licence is allowlisted and recorded with a
// URL. It runs at install time (API and worker) and again at engine load.
func Gate(e Entry) error {
	if !LicenceAllowed(e.Licence.SPDX) {
		return fmt.Errorf("%w: %s is licensed %q, which is not in the allowlist %v", ErrLicenceRefused, e.Name, e.Licence.SPDX, LicenceAllowlist)
	}
	if e.Licence.URL == "" {
		return fmt.Errorf("%w: %s has no licence URL recorded", ErrLicenceRefused, e.Name)
	}
	return nil
}

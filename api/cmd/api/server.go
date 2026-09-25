package main

import (
	"loomtale/api/internal/assetsapi"
	"loomtale/api/internal/auditapi"
	"loomtale/api/internal/authapi"
	"loomtale/api/internal/health"
	"loomtale/api/internal/httpapi/gen"
)

// server composes every domain handler into the single type
// gen.StrictServerInterface requires. Each domain package implements only
// its own methods; Go's method promotion through embedding does the rest,
// so no method is ever redefined here.
type server struct {
	*health.Handler
	*authapi.AuthAPI
	*assetsapi.AssetsAPI
	*auditapi.AuditAPI
}

var _ gen.StrictServerInterface = (*server)(nil)

package main

import (
	"loomtale/api/internal/analyticsapi"
	"loomtale/api/internal/assetsapi"
	"loomtale/api/internal/auditapi"
	"loomtale/api/internal/authapi"
	"loomtale/api/internal/channelsapi"
	"loomtale/api/internal/characters"
	"loomtale/api/internal/health"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/library"
	"loomtale/api/internal/media"
	"loomtale/api/internal/modelsapi"
	"loomtale/api/internal/pipelineapi"
	"loomtale/api/internal/presets"
	"loomtale/api/internal/render"
	"loomtale/api/internal/scenes"
	"loomtale/api/internal/settingsapi"
	"loomtale/api/internal/story"
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
	*pipelineapi.PipelineAPI
	*settingsapi.SettingsAPI
	*story.StoryAPI
	*modelsapi.ModelsAPI
	*presets.PresetsAPI
	*characters.CharactersAPI
	*scenes.ScenesAPI
	*media.MediaAPI
	*channelsapi.ChannelsAPI
	*render.RenderAPI
	*library.LibraryAPI
	*analyticsapi.AnalyticsAPI
}

var _ gen.StrictServerInterface = (*server)(nil)

package story

// Pipeline step kinds this package registers a handler for.
const (
	KindOutline    = "llm.outline"
	KindExpandBeat = "llm.expand_beat"
	KindContinue   = "llm.continue"
	KindRewrite    = "llm.rewrite"
	KindTranslate  = "llm.translate"
	KindSummarise  = "llm.summarise"
	KindBibleSeed  = "llm.bible_seed"
)

// Pipeline step scope kinds.
const (
	ScopeSeries  = "series"
	ScopeEpisode = "episode"
)

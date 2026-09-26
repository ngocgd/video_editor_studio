package story

// outlineSchema constrains llm.outline's structured JSON output: an
// ordered array of beats. Passed as llm.Request.JSONSchema so
// llm.GenerateStructured validates (and retries once on failure) before
// this package ever parses it.
const outlineSchema = `{
  "type": "array",
  "minItems": 1,
  "items": {
    "type": "object",
    "required": ["id", "summary", "targetWords"],
    "properties": {
      "id": {"type": "string", "minLength": 1},
      "summary": {"type": "string", "minLength": 1},
      "targetWords": {"type": "integer", "minimum": 1}
    },
    "additionalProperties": false
  }
}`

// bibleSeedSchema constrains llm.bible_seed's structured JSON output: a
// map of section name to content, one entry per bible section. Prose
// sections are plain text; the glossary is an array of term renderings,
// the same shape the bible editor's glossary table reads and writes.
const bibleSeedSchema = `{
  "type": "object",
  "required": ["world", "cultivation_realms", "arcs", "style_guide", "running_summary", "glossary"],
  "properties": {
    "world": {"type": "string"},
    "cultivation_realms": {"type": "string"},
    "arcs": {"type": "string"},
    "style_guide": {"type": "string"},
    "running_summary": {"type": "string"},
    "glossary": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["termZh", "en", "vi"],
        "properties": {
          "termZh": {"type": "string", "minLength": 1},
          "en": {"type": "string"},
          "vi": {"type": "string"}
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}`

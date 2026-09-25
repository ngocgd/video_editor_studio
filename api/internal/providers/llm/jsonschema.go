package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrSchemaValidation is wrapped by ValidateJSON's returned error so
// callers can distinguish "the model's JSON failed the schema" from a
// transport/provider error.
var ErrSchemaValidation = errors.New("llm: response failed JSON schema validation")

// ValidateJSON parses text as JSON and validates it against schemaText (a
// JSON Schema document). A syntactically invalid schema is a programmer
// error, so it panics; a validation failure wraps ErrSchemaValidation with
// the schema validator's own explanation.
func ValidateJSON(schemaText, text string) error {
	compiler := jsonschema.NewCompiler()
	var schemaDoc any
	if err := json.Unmarshal([]byte(schemaText), &schemaDoc); err != nil {
		panic(fmt.Sprintf("llm: invalid JSON schema: %v", err))
	}
	if err := compiler.AddResource("request.json", schemaDoc); err != nil {
		panic(fmt.Sprintf("llm: invalid JSON schema: %v", err))
	}
	schema, err := compiler.Compile("request.json")
	if err != nil {
		panic(fmt.Sprintf("llm: invalid JSON schema: %v", err))
	}

	var instance any
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	if err := dec.Decode(&instance); err != nil {
		return fmt.Errorf("%w: response is not valid JSON: %v", ErrSchemaValidation, err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}
	return nil
}

// GenerateStructured calls gen once, and on a JSON-schema validation
// failure retries exactly once with a corrective follow-up message
// appended, per the phase 4 contract ("one retry"). gen is normally
// Provider.Generate; passed in so this helper has no direct Provider
// dependency and is trivially testable with a stub.
func GenerateStructured(ctx context.Context, req Request, gen func(context.Context, Request) (Response, error)) (Response, error) {
	resp, err := gen(ctx, req)
	if err != nil {
		return Response{}, err
	}
	if req.JSONSchema == "" {
		return resp, nil
	}
	if verr := ValidateJSON(req.JSONSchema, resp.Text); verr == nil {
		return resp, nil
	} else if !errors.Is(verr, ErrSchemaValidation) {
		return Response{}, verr
	} else {
		retryReq := req
		retryReq.Messages = append(append([]Message{}, req.Messages...), Message{
			Role: "user",
			Text: "Your previous response did not match the required JSON schema (" + verr.Error() + "). Reply again with only corrected JSON matching the schema.",
		})
		return gen(ctx, retryReq)
	}
}

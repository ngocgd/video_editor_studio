package llm

import (
	"context"
	"errors"
	"testing"

	"loomtale/api/internal/pipeline"
)

const testSchema = `{"type":"object","required":["outline"],"properties":{"outline":{"type":"string"}}}`

func TestValidateJSONAcceptsMatchingDocument(t *testing.T) {
	if err := ValidateJSON(testSchema, `{"outline":"a story"}`); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJSONRejectsSchemaMismatch(t *testing.T) {
	err := ValidateJSON(testSchema, `{"wrong":"field"}`)
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation, got %v", err)
	}
}

func TestValidateJSONRejectsMalformedJSON(t *testing.T) {
	err := ValidateJSON(testSchema, `{not json`)
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation, got %v", err)
	}
}

func TestValidateJSONRejectsTrailingContent(t *testing.T) {
	err := ValidateJSON(testSchema, `{"outline":"a story"} and then some prose`)
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation for trailing content, got %v", err)
	}
}

func TestValidateJSONAcceptsTrailingWhitespaceOnly(t *testing.T) {
	if err := ValidateJSON(testSchema, "{\"outline\":\"a story\"}\n  \n"); err != nil {
		t.Fatalf("trailing whitespace should be accepted, got %v", err)
	}
}

func TestGenerateStructuredReturnsFirstResponseWhenValid(t *testing.T) {
	calls := 0
	gen := func(context.Context, Request) (Response, error) {
		calls++
		return Response{Text: `{"outline":"ok"}`}, nil
	}
	resp, err := GenerateStructured(context.Background(), Request{JSONSchema: testSchema}, gen)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"outline":"ok"}` || calls != 1 {
		t.Fatalf("resp=%+v calls=%d", resp, calls)
	}
}

func TestGenerateStructuredRetriesOnceThenSucceeds(t *testing.T) {
	calls := 0
	gen := func(_ context.Context, req Request) (Response, error) {
		calls++
		if calls == 1 {
			return Response{Text: `not json`}, nil
		}
		return Response{Text: `{"outline":"fixed"}`}, nil
	}
	resp, err := GenerateStructured(context.Background(), Request{JSONSchema: testSchema}, gen)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"outline":"fixed"}` || calls != 2 {
		t.Fatalf("resp=%+v calls=%d", resp, calls)
	}
}

// TestGenerateStructuredValidatesTheRetryToo is H5: a second consecutive
// schema failure must not be handed back to the caller as if it were
// valid.
func TestGenerateStructuredValidatesTheRetryToo(t *testing.T) {
	calls := 0
	gen := func(context.Context, Request) (Response, error) {
		calls++
		return Response{Text: `still not json`}, nil
	}
	_, err := GenerateStructured(context.Background(), Request{JSONSchema: testSchema}, gen)
	if err == nil {
		t.Fatal("expected an error when the retry also fails schema validation")
	}
	if !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected pipeline.ErrValidation, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected exactly one retry (2 calls total), got %d", calls)
	}
}

func TestGenerateStructuredSkipsValidationWhenNoSchemaRequested(t *testing.T) {
	gen := func(context.Context, Request) (Response, error) {
		return Response{Text: "plain text, not JSON"}, nil
	}
	resp, err := GenerateStructured(context.Background(), Request{}, gen)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "plain text, not JSON" {
		t.Fatalf("resp = %+v", resp)
	}
}

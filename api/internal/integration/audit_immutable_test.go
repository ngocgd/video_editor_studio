//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
)

func TestAuditLogRejectsUpdateAsOwnerRole(t *testing.T) {
	pool := ownerPool(t)
	_, err := pool.Exec(context.Background(), "UPDATE audit_log SET action = 'tampered' WHERE true")
	if err == nil {
		t.Fatal("expected UPDATE on audit_log to fail even for the owner role")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected the append-only trigger message, got: %v", err)
	}
}

func TestAuditLogRejectsUpdateAsAppRole(t *testing.T) {
	pool := appPool(t)
	_, err := pool.Exec(context.Background(), "UPDATE audit_log SET action = 'tampered' WHERE true")
	if err == nil {
		t.Fatal("expected UPDATE on audit_log to fail for the app role")
	}
}

func TestAuditLogRejectsDeleteAsOwnerRole(t *testing.T) {
	pool := ownerPool(t)
	_, err := pool.Exec(context.Background(), "DELETE FROM audit_log WHERE true")
	if err == nil {
		t.Fatal("expected DELETE on audit_log to fail even for the owner role")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected the append-only trigger message, got: %v", err)
	}
}

func TestAppRoleHasNoDDLRights(t *testing.T) {
	pool := appPool(t)
	_, err := pool.Exec(context.Background(), "DROP TABLE users")
	if err == nil {
		t.Fatal("expected the app role to be refused DDL rights")
	}
}

package openapivisibility

import (
	"encoding/json"
	"testing"
)

func TestFilterRemovesInternalOperationsAndUnreachableSchemas(t *testing.T) {
	t.Parallel()

	input := []byte(`{
  "openapi": "3.1.0",
  "paths": {
    "/task/create": {"post": {"operationId": "libops.v1.TaskService.CreateTask", "tags": ["libops.v1.TaskService"], "security": [{"oauth2": ["write:organization"]}], "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/PublicRequest"}}}}}},
    "/task/log": {"post": {"operationId": "libops.v1.TaskService.AppendTaskLog", "tags": ["libops.v1.TaskService"]}},
    "/admin/account": {"get": {"operationId": "libops.v1.AdminAccountService.GetAccount.get", "tags": ["libops.v1.AdminAccountService"]}}
  },
  "tags": [{"name": "libops.v1.TaskService"}, {"name": "libops.v1.AdminAccountService"}],
  "components": {"schemas": {
    "PublicRequest": {"properties": {"nested": {"$ref": "#/components/schemas/PublicNested"}}},
    "PublicNested": {"type": "object"},
    "AdminSecret": {"type": "object"}
  }, "securitySchemes": {"oauth2": {"type": "oauth2", "flows": {"authorizationCode": {"scopes": {"write:organization": "Write", "admin:system": "Admin"}}}}}}
}`)

	output, err := Filter(input)
	if err != nil {
		t.Fatalf("Filter() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	paths := got["paths"].(map[string]any)
	if _, exists := paths["/task/create"]; !exists {
		t.Error("public operation was removed")
	}
	if _, exists := paths["/task/log"]; exists {
		t.Error("internal method on public service was retained")
	}
	if _, exists := paths["/admin/account"]; exists {
		t.Error("internal service operation was retained")
	}
	schemas := got["components"].(map[string]any)["schemas"].(map[string]any)
	if _, exists := schemas["PublicNested"]; !exists {
		t.Error("transitively referenced public schema was removed")
	}
	if _, exists := schemas["AdminSecret"]; exists {
		t.Error("unreachable internal schema was retained")
	}
	tags := got["tags"].([]any)
	if len(tags) != 1 {
		t.Errorf("retained %d tags; want 1", len(tags))
	}
	securitySchemes := got["components"].(map[string]any)["securitySchemes"].(map[string]any)
	flows := securitySchemes["oauth2"].(map[string]any)["flows"].(map[string]any)
	scopes := flows["authorizationCode"].(map[string]any)["scopes"].(map[string]any)
	if _, exists := scopes["write:organization"]; !exists {
		t.Error("OAuth scope used by public operation was removed")
	}
	if _, exists := scopes["admin:system"]; exists {
		t.Error("internal-only OAuth scope was retained")
	}
}

func TestFilterFailsClosedForUnknownOperation(t *testing.T) {
	t.Parallel()

	_, err := Filter([]byte(`{"paths":{"/new":{"post":{"operationId":"libops.v1.MissingService.New"}}}}`))
	if err == nil {
		t.Fatal("Filter() error = nil; want unknown descriptor error")
	}
}

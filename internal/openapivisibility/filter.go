// Package openapivisibility derives the customer OpenAPI document from the
// complete generated document and protobuf visibility annotations.
package openapivisibility

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	_ "github.com/libops/proto/libops/v1"
	optionsv1 "github.com/libops/proto/libops/v1/options"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var httpMethods = map[string]bool{
	"delete":  true,
	"get":     true,
	"head":    true,
	"options": true,
	"patch":   true,
	"post":    true,
	"put":     true,
	"trace":   true,
}

// Filter returns a public OpenAPI document. Unknown or unannotated operations
// are errors so a new RPC cannot become customer-visible by accident.
func Filter(input []byte) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		return nil, fmt.Errorf("decode OpenAPI document: %w", err)
	}

	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI document has no paths object")
	}
	usedTags := map[string]bool{}
	for path, rawItem := range paths {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q is not an object", path)
		}
		for method, rawOperation := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("operation %s %s is not an object", method, path)
			}
			operationID, _ := operation["operationId"].(string)
			visibility, err := operationVisibility(operationID)
			if err != nil {
				return nil, fmt.Errorf("operation %s %s: %w", method, path, err)
			}
			if visibility != optionsv1.ApiVisibility_API_VISIBILITY_PUBLIC {
				delete(item, method)
				continue
			}
			if tags, ok := operation["tags"].([]any); ok {
				for _, rawTag := range tags {
					if tag, valid := rawTag.(string); valid {
						usedTags[tag] = true
					}
				}
			}
		}
		if !containsOperation(item) {
			delete(paths, path)
		}
	}

	filterTags(document, usedTags)
	filterOAuthScopes(document)
	if err := pruneSchemas(document); err != nil {
		return nil, err
	}

	output, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode public OpenAPI document: %w", err)
	}
	return append(output, '\n'), nil
}

func filterOAuthScopes(document map[string]any) {
	used := map[string]bool{}
	paths, _ := document["paths"].(map[string]any)
	for _, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOperation := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			operation, _ := rawOperation.(map[string]any)
			security, _ := operation["security"].([]any)
			for _, rawRequirement := range security {
				requirement, _ := rawRequirement.(map[string]any)
				for _, rawScopes := range requirement {
					scopes, _ := rawScopes.([]any)
					for _, rawScope := range scopes {
						if scope, ok := rawScope.(string); ok {
							used[scope] = true
						}
					}
				}
			}
		}
	}

	components, _ := document["components"].(map[string]any)
	securitySchemes, _ := components["securitySchemes"].(map[string]any)
	for _, rawScheme := range securitySchemes {
		scheme, _ := rawScheme.(map[string]any)
		if scheme["type"] != "oauth2" {
			continue
		}
		flows, _ := scheme["flows"].(map[string]any)
		for _, rawFlow := range flows {
			flow, _ := rawFlow.(map[string]any)
			scopes, _ := flow["scopes"].(map[string]any)
			for scope := range scopes {
				if !used[scope] {
					delete(scopes, scope)
				}
			}
		}
	}
}

func operationVisibility(operationID string) (optionsv1.ApiVisibility, error) {
	operationID = strings.TrimSuffix(operationID, ".get")
	separator := strings.LastIndexByte(operationID, '.')
	if separator <= 0 || separator == len(operationID)-1 {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("invalid operationId %q", operationID)
	}
	serviceName := protoreflect.FullName(operationID[:separator])
	methodName := protoreflect.Name(operationID[separator+1:])
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("find service %q: %w", serviceName, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("%q is not a service", serviceName)
	}
	serviceVisibility := getServiceVisibility(service)
	if serviceVisibility == optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED {
		return serviceVisibility, fmt.Errorf("service %q has no explicit API visibility", serviceName)
	}
	method := service.Methods().ByName(methodName)
	if method == nil {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("service %q has no method %q", serviceName, methodName)
	}
	methodOptions, _ := method.Options().(*descriptorpb.MethodOptions)
	if methodOptions == nil || !proto.HasExtension(methodOptions, optionsv1.E_MethodApiVisibility) {
		return serviceVisibility, nil
	}
	methodVisibility, ok := proto.GetExtension(methodOptions, optionsv1.E_MethodApiVisibility).(optionsv1.ApiVisibility)
	if !ok || methodVisibility == optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("method %q has an invalid API visibility", method.FullName())
	}
	if serviceVisibility == optionsv1.ApiVisibility_API_VISIBILITY_INTERNAL && methodVisibility == optionsv1.ApiVisibility_API_VISIBILITY_PUBLIC {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, fmt.Errorf("method %q cannot widen an internal service to public", method.FullName())
	}
	return methodVisibility, nil
}

func getServiceVisibility(service protoreflect.ServiceDescriptor) optionsv1.ApiVisibility {
	serviceOptions, _ := service.Options().(*descriptorpb.ServiceOptions)
	if serviceOptions == nil || !proto.HasExtension(serviceOptions, optionsv1.E_ServiceApiVisibility) {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED
	}
	visibility, ok := proto.GetExtension(serviceOptions, optionsv1.E_ServiceApiVisibility).(optionsv1.ApiVisibility)
	if !ok {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED
	}
	return visibility
}

func containsOperation(item map[string]any) bool {
	for key := range item {
		if httpMethods[strings.ToLower(key)] {
			return true
		}
	}
	return false
}

func filterTags(document map[string]any, used map[string]bool) {
	tags, ok := document["tags"].([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(tags))
	for _, rawTag := range tags {
		tag, ok := rawTag.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tag["name"].(string)
		if used[name] {
			filtered = append(filtered, tag)
		}
	}
	document["tags"] = filtered
}

func pruneSchemas(document map[string]any) error {
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil
	}

	needed := map[string]bool{}
	collectSchemaRefs(document["paths"], needed)
	queue := make([]string, 0, len(needed))
	for name := range needed {
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		schema, exists := schemas[name]
		if !exists {
			return fmt.Errorf("public OpenAPI document references missing schema %q", name)
		}
		found := map[string]bool{}
		collectSchemaRefs(schema, found)
		for nested := range found {
			if !needed[nested] {
				needed[nested] = true
				queue = append(queue, nested)
			}
		}
	}

	retained := make(map[string]any, len(needed))
	names := make([]string, 0, len(needed))
	for name := range needed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		retained[name] = schemas[name]
	}
	components["schemas"] = retained
	return nil
}

func collectSchemaRefs(value any, found map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "$ref" {
				if ref, ok := nested.(string); ok {
					const prefix = "#/components/schemas/"
					if strings.HasPrefix(ref, prefix) {
						name := strings.TrimPrefix(ref, prefix)
						name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
						found[name] = true
					}
				}
				continue
			}
			collectSchemaRefs(nested, found)
		}
	case []any:
		for _, nested := range typed {
			collectSchemaRefs(nested, found)
		}
	}
}

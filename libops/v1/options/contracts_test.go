package options_test

import (
	"strings"
	"testing"

	_ "github.com/libops/proto/libops/v1"
	optionsv1 "github.com/libops/proto/libops/v1/options"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestEveryLibOpsServiceDeclaresAPIVisibility(t *testing.T) {
	t.Parallel()

	serviceCount := 0
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(file.Path(), "libops/v1/") {
			return true
		}
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			serviceCount++
			service := services.Get(i)
			declaredVisibility := serviceVisibility(service)
			if declaredVisibility == optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED {
				t.Errorf("service %s must declare service_api_visibility", service.FullName())
			}
			methods := service.Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				visibility, explicit := explicitMethodVisibility(method)
				if !explicit {
					continue
				}
				if visibility == optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED {
					t.Errorf("method %s has an unspecified method_api_visibility override", method.FullName())
				}
				if declaredVisibility == optionsv1.ApiVisibility_API_VISIBILITY_INTERNAL && visibility == optionsv1.ApiVisibility_API_VISIBILITY_PUBLIC {
					t.Errorf("method %s cannot widen an internal service to public", method.FullName())
				}
			}
		}
		return true
	})

	if serviceCount != 28 {
		t.Fatalf("checked %d services; want 28 (update the contract count when adding a service)", serviceCount)
	}
}

func TestInternalAPIContract(t *testing.T) {
	t.Parallel()

	tests := map[protoreflect.FullName]optionsv1.ApiVisibility{
		"libops.v1.AdminAccountService.GetAccount": optionsv1.ApiVisibility_API_VISIBILITY_INTERNAL,
		"libops.v1.TaskService.AppendTaskLog":      optionsv1.ApiVisibility_API_VISIBILITY_INTERNAL,
		"libops.v1.TaskService.CreateTask":         optionsv1.ApiVisibility_API_VISIBILITY_PUBLIC,
	}
	for name, want := range tests {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		method, ok := descriptor.(protoreflect.MethodDescriptor)
		if !ok {
			t.Fatalf("%s is %T, want method descriptor", name, descriptor)
		}
		if got := methodVisibility(method); got != want {
			t.Errorf("%s visibility = %s; want %s", name, got, want)
		}
	}
}

func TestAssistantPlaygroundMethodsCannotAcceptSensitiveFields(t *testing.T) {
	t.Parallel()

	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(file.Path(), "libops/v1/") {
			return true
		}
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				if assistantEnabled(method) && messageContainsSensitiveField(method.Input(), map[protoreflect.FullName]bool{}) {
					t.Errorf("assistant playground method %s transitively accepts a sensitive field", method.FullName())
				}
			}
		}
		return true
	})
}

func serviceVisibility(service protoreflect.ServiceDescriptor) optionsv1.ApiVisibility {
	options, ok := service.Options().(*descriptorpb.ServiceOptions)
	if !ok || !proto.HasExtension(options, optionsv1.E_ServiceApiVisibility) {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED
	}
	visibility, ok := proto.GetExtension(options, optionsv1.E_ServiceApiVisibility).(optionsv1.ApiVisibility)
	if !ok {
		return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED
	}
	return visibility
}

func methodVisibility(method protoreflect.MethodDescriptor) optionsv1.ApiVisibility {
	if visibility, explicit := explicitMethodVisibility(method); explicit {
		return visibility
	}
	return serviceVisibility(method.Parent().(protoreflect.ServiceDescriptor))
}

func explicitMethodVisibility(method protoreflect.MethodDescriptor) (optionsv1.ApiVisibility, bool) {
	options, ok := method.Options().(*descriptorpb.MethodOptions)
	if ok && proto.HasExtension(options, optionsv1.E_MethodApiVisibility) {
		if visibility, valid := proto.GetExtension(options, optionsv1.E_MethodApiVisibility).(optionsv1.ApiVisibility); valid {
			return visibility, true
		}
	}
	return optionsv1.ApiVisibility_API_VISIBILITY_UNSPECIFIED, false
}

func assistantEnabled(method protoreflect.MethodDescriptor) bool {
	options, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || !proto.HasExtension(options, optionsv1.E_AssistantPlayground) {
		return false
	}
	enabled, _ := proto.GetExtension(options, optionsv1.E_AssistantPlayground).(bool)
	return enabled
}

func messageContainsSensitiveField(message protoreflect.MessageDescriptor, visiting map[protoreflect.FullName]bool) bool {
	if visiting[message.FullName()] {
		return false
	}
	visiting[message.FullName()] = true
	defer delete(visiting, message.FullName())

	fields := message.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		options, ok := field.Options().(*descriptorpb.FieldOptions)
		if ok && proto.HasExtension(options, optionsv1.E_Sensitive) {
			if sensitive, valid := proto.GetExtension(options, optionsv1.E_Sensitive).(bool); valid && sensitive {
				return true
			}
		}
		if nested := field.Message(); nested != nil && messageContainsSensitiveField(nested, visiting) {
			return true
		}
	}
	return false
}

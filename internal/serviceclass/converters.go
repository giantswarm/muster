package serviceclass

import (
	"encoding/json"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

// convertCRDToServiceClass converts a ServiceClass CRD to api.ServiceClass
func convertCRDToServiceClass(sc *musterv1alpha1.ServiceClass) api.ServiceClass {
	serviceClass := api.ServiceClass{
		Name:        sc.ObjectMeta.Name,
		Description: sc.Spec.Description,
		Args:        convertArgsFromCRD(sc.Spec.Args),
		ServiceConfig: api.ServiceConfig{
			DefaultName:    sc.Spec.ServiceConfig.DefaultName,
			Dependencies:   sc.Spec.ServiceConfig.Dependencies,
			LifecycleTools: convertLifecycleToolsFromCRD(sc.Spec.ServiceConfig.LifecycleTools),
			HealthCheck:    convertHealthCheckConfigFromCRD(sc.Spec.ServiceConfig.HealthCheck),
			Timeout:        convertTimeoutConfigFromCRD(sc.Spec.ServiceConfig.Timeout),
			Outputs:        convertStringMapFromCRD(sc.Spec.ServiceConfig.Outputs),
		},
		// Map CRD validation status to the Available field.
		// Note: Tool availability for execution is computed per-session at runtime
		// and not stored in the CRD status (see ADR 007).
		Available: sc.Status.Valid,
	}

	return serviceClass
}

// convertArgsFromCRD converts CRD argument definitions to API argument definitions
func convertArgsFromCRD(args map[string]musterv1alpha1.ArgDefinition) map[string]api.ArgDefinition {
	if args == nil {
		return nil
	}
	result := make(map[string]api.ArgDefinition)
	for name, arg := range args {
		result[name] = api.ArgDefinition{
			Type:        arg.Type,
			Required:    arg.Required,
			Default:     convertRawExtensionToInterface(arg.Default),
			Description: arg.Description,
		}
	}
	return result
}

// convertLifecycleToolsFromCRD converts CRD lifecycle tools to API lifecycle tools
func convertLifecycleToolsFromCRD(tools musterv1alpha1.LifecycleTools) api.LifecycleTools {
	result := api.LifecycleTools{
		Start: convertToolCallFromCRD(tools.Start),
		Stop:  convertToolCallFromCRD(tools.Stop),
	}
	if tools.Restart != nil {
		restart := convertToolCallFromCRD(*tools.Restart)
		result.Restart = &restart
	}
	if tools.HealthCheck != nil {
		healthCheck := convertHealthCheckToolCallFromCRD(*tools.HealthCheck)
		result.HealthCheck = &healthCheck
	}
	if tools.Status != nil {
		status := convertToolCallFromCRD(*tools.Status)
		result.Status = &status
	}
	return result
}

// convertToolCallFromCRD converts a CRD tool call to API tool call
func convertToolCallFromCRD(tool musterv1alpha1.ToolCall) api.ToolCall {
	return api.ToolCall{
		Tool:    tool.Tool,
		Args:    convertRawExtensionMapToInterface(tool.Args),
		Outputs: tool.Outputs,
	}
}

// convertHealthCheckToolCallFromCRD converts a CRD health check tool call to API health check tool call
func convertHealthCheckToolCallFromCRD(tool musterv1alpha1.HealthCheckToolCall) api.HealthCheckToolCall {
	result := api.HealthCheckToolCall{
		Tool: tool.Tool,
		Args: convertRawExtensionMapToInterface(tool.Args),
	}
	if tool.Expect != nil {
		expect := convertHealthCheckExpectationFromCRD(*tool.Expect)
		result.Expect = &expect
	}
	if tool.ExpectNot != nil {
		expectNot := convertHealthCheckExpectationFromCRD(*tool.ExpectNot)
		result.ExpectNot = &expectNot
	}
	return result
}

// convertHealthCheckExpectationFromCRD converts CRD health check expectation to API expectation
func convertHealthCheckExpectationFromCRD(exp musterv1alpha1.HealthCheckExpectation) api.HealthCheckExpectation {
	return api.HealthCheckExpectation{
		Success:  exp.Success,
		JsonPath: convertRawExtensionMapToInterface(exp.JSONPath),
	}
}

// convertHealthCheckConfigFromCRD converts CRD health check config to API health check config
func convertHealthCheckConfigFromCRD(config *musterv1alpha1.HealthCheckConfig) api.HealthCheckConfig {
	if config == nil {
		return api.HealthCheckConfig{}
	}

	interval, _ := time.ParseDuration(config.Interval)
	return api.HealthCheckConfig{
		Enabled:          config.Enabled,
		Interval:         interval,
		FailureThreshold: config.FailureThreshold,
		SuccessThreshold: config.SuccessThreshold,
	}
}

// convertTimeoutConfigFromCRD converts CRD timeout config to API timeout config
func convertTimeoutConfigFromCRD(config *musterv1alpha1.TimeoutConfig) api.TimeoutConfig {
	if config == nil {
		return api.TimeoutConfig{}
	}

	create, _ := time.ParseDuration(config.Create)
	delete, _ := time.ParseDuration(config.Delete)
	healthCheck, _ := time.ParseDuration(config.HealthCheck)

	return api.TimeoutConfig{
		Create:      create,
		Delete:      delete,
		HealthCheck: healthCheck,
	}
}

// convertStringMapFromCRD converts a string map from CRD to interface map for API
func convertStringMapFromCRD(m map[string]string) map[string]interface{} {
	if m == nil {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// convertRawExtensionMapToInterface converts a map of RawExtension to interface map
func convertRawExtensionMapToInterface(m map[string]*runtime.RawExtension) map[string]interface{} {
	if m == nil {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = convertRawExtensionToInterface(v)
	}
	return result
}

// convertRawExtensionToInterface converts a RawExtension to interface{}
func convertRawExtensionToInterface(raw *runtime.RawExtension) interface{} {
	if raw == nil {
		return nil
	}

	// If Raw is empty, return nil
	if len(raw.Raw) == 0 {
		return nil
	}

	// Try to unmarshal as JSON first
	var result interface{}
	if err := json.Unmarshal(raw.Raw, &result); err == nil {
		return result
	}

	// If JSON unmarshaling fails, treat as raw string
	// This handles cases where the YAML contains raw strings like "{{ .args.repository_url }}"
	rawStr := string(raw.Raw)

	// Remove quotes if it's a quoted string
	if len(rawStr) >= 2 && rawStr[0] == '"' && rawStr[len(rawStr)-1] == '"' {
		// Try to unmarshal as JSON string to handle escaping
		var unquoted string
		if err := json.Unmarshal(raw.Raw, &unquoted); err == nil {
			return unquoted
		}
		// If that fails, just remove quotes manually
		result := rawStr[1 : len(rawStr)-1]
		return result
	}

	// Return as-is if not quoted
	return rawStr
}

// convertArgsRequestToCRD converts API argument definitions to CRD argument definitions
func convertArgsRequestToCRD(args map[string]api.ArgDefinition) map[string]musterv1alpha1.ArgDefinition {
	if args == nil {
		return nil
	}
	result := make(map[string]musterv1alpha1.ArgDefinition)
	for name, arg := range args {
		result[name] = musterv1alpha1.ArgDefinition{
			Type:        arg.Type,
			Required:    arg.Required,
			Default:     convertInterfaceToRawExtension(arg.Default),
			Description: arg.Description,
		}
	}
	return result
}

// convertServiceConfigRequestToCRD converts API service config to CRD service config
func convertServiceConfigRequestToCRD(config api.ServiceConfig) musterv1alpha1.ServiceConfig {
	return musterv1alpha1.ServiceConfig{
		DefaultName:    config.DefaultName,
		Dependencies:   config.Dependencies,
		LifecycleTools: convertLifecycleToolsRequestToCRD(config.LifecycleTools),
		HealthCheck:    convertHealthCheckConfigRequestToCRD(config.HealthCheck),
		Timeout:        convertTimeoutConfigRequestToCRD(config.Timeout),
		Outputs:        convertOutputsRequestToCRD(config.Outputs),
	}
}

// convertLifecycleToolsRequestToCRD converts API lifecycle tools to CRD lifecycle tools
func convertLifecycleToolsRequestToCRD(tools api.LifecycleTools) musterv1alpha1.LifecycleTools {
	result := musterv1alpha1.LifecycleTools{
		Start: convertToolCallRequestToCRD(tools.Start),
		Stop:  convertToolCallRequestToCRD(tools.Stop),
	}
	if tools.Restart != nil {
		restart := convertToolCallRequestToCRD(*tools.Restart)
		result.Restart = &restart
	}
	if tools.HealthCheck != nil {
		healthCheck := convertHealthCheckToolCallRequestToCRD(*tools.HealthCheck)
		result.HealthCheck = &healthCheck
	}
	if tools.Status != nil {
		status := convertToolCallRequestToCRD(*tools.Status)
		result.Status = &status
	}
	return result
}

// convertToolCallRequestToCRD converts API tool call to CRD tool call
func convertToolCallRequestToCRD(tool api.ToolCall) musterv1alpha1.ToolCall {
	return musterv1alpha1.ToolCall{
		Tool:    tool.Tool,
		Args:    convertArgsMapToRawExtension(tool.Args),
		Outputs: tool.Outputs,
	}
}

// convertHealthCheckToolCallRequestToCRD converts API health check tool call to CRD health check tool call
func convertHealthCheckToolCallRequestToCRD(tool api.HealthCheckToolCall) musterv1alpha1.HealthCheckToolCall {
	result := musterv1alpha1.HealthCheckToolCall{
		Tool: tool.Tool,
		Args: convertArgsMapToRawExtension(tool.Args),
	}
	if tool.Expect != nil {
		expect := convertHealthCheckExpectationRequestToCRD(*tool.Expect)
		result.Expect = &expect
	}
	if tool.ExpectNot != nil {
		expectNot := convertHealthCheckExpectationRequestToCRD(*tool.ExpectNot)
		result.ExpectNot = &expectNot
	}
	return result
}

// convertHealthCheckExpectationRequestToCRD converts API health check expectation to CRD expectation
func convertHealthCheckExpectationRequestToCRD(exp api.HealthCheckExpectation) musterv1alpha1.HealthCheckExpectation {
	return musterv1alpha1.HealthCheckExpectation{
		Success:  exp.Success,
		JSONPath: convertArgsMapToRawExtension(exp.JsonPath),
	}
}

// convertHealthCheckConfigRequestToCRD converts API health check config to CRD health check config
func convertHealthCheckConfigRequestToCRD(config api.HealthCheckConfig) *musterv1alpha1.HealthCheckConfig {
	if isHealthCheckConfigEmpty(config) {
		return nil
	}

	return &musterv1alpha1.HealthCheckConfig{
		Enabled:          config.Enabled,
		Interval:         config.Interval.String(),
		FailureThreshold: config.FailureThreshold,
		SuccessThreshold: config.SuccessThreshold,
	}
}

// convertTimeoutConfigRequestToCRD converts API timeout config to CRD timeout config
func convertTimeoutConfigRequestToCRD(config api.TimeoutConfig) *musterv1alpha1.TimeoutConfig {
	if isTimeoutConfigEmpty(config) {
		return nil
	}

	return &musterv1alpha1.TimeoutConfig{
		Create:      config.Create.String(),
		Delete:      config.Delete.String(),
		HealthCheck: config.HealthCheck.String(),
	}
}

// convertOutputsRequestToCRD converts API outputs to CRD outputs
func convertOutputsRequestToCRD(outputs map[string]interface{}) map[string]string {
	if outputs == nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range outputs {
		if str, ok := v.(string); ok {
			result[k] = str
		}
	}
	return result
}

// convertArgsMapToRawExtension converts interface map to RawExtension map
func convertArgsMapToRawExtension(args map[string]interface{}) map[string]*runtime.RawExtension {
	if args == nil {
		return nil
	}
	result := make(map[string]*runtime.RawExtension)
	for k, v := range args {
		result[k] = convertInterfaceToRawExtension(v)
	}
	return result
}

// convertInterfaceToRawExtension converts an interface{} to RawExtension
func convertInterfaceToRawExtension(v interface{}) *runtime.RawExtension {
	if v == nil {
		return nil
	}

	// Try to marshal as JSON
	data, err := json.Marshal(v)
	if err != nil {
		// If marshaling fails, treat as string
		if str, ok := v.(string); ok {
			data = []byte(`"` + str + `"`)
		} else {
			log.Printf("Warning: Failed to convert value to RawExtension: %v", err)
			return nil
		}
	}

	return &runtime.RawExtension{
		Raw: data,
	}
}

// Helper functions for checking if configs are empty
func isHealthCheckConfigEmpty(config api.HealthCheckConfig) bool {
	return !config.Enabled &&
		config.Interval == 0 &&
		config.FailureThreshold == 0 &&
		config.SuccessThreshold == 0
}

func isTimeoutConfigEmpty(config api.TimeoutConfig) bool {
	return config.Create == 0 &&
		config.Delete == 0 &&
		config.HealthCheck == 0
}

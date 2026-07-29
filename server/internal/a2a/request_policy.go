package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// RequestPolicy is one canonical policy requirement for a supported A2A
// method.
type RequestPolicy struct {
	CanonicalMethod string
	Action          string
	ResourceID      string
	Write           bool
	Risky           bool
	MessageID       string
}

// DecodeJSONRPCRequest parses the JSON-RPC envelope. Recognized fields are
// case-sensitive, removed legacy fields are rejected, and genuinely unknown
// fields are ignored as required for A2A forward compatibility. Params remain
// raw until the canonical method selects their concrete schema.
func DecodeJSONRPCRequest(raw []byte) (JSONRPCRequest, error) {
	var request JSONRPCRequest
	if err := decodeExactJSON(raw, &request); err != nil {
		return JSONRPCRequest{}, err
	}
	return request, nil
}

// CanonicalMethod returns the exact A2A 1.0 JSON-RPC method name. ChronoDesk
// intentionally exposes no pre-1.0 aliases.
func CanonicalMethod(method string) string {
	switch method {
	case "SendMessage":
		return "SendMessage"
	case "SendStreamingMessage":
		return "SendStreamingMessage"
	case "GetTask":
		return "GetTask"
	case "ListTasks":
		return "ListTasks"
	case "CancelTask":
		return "CancelTask"
	case "SubscribeToTask":
		return "SubscribeToTask"
	case "CreateTaskPushNotificationConfig":
		return "CreateTaskPushNotificationConfig"
	case "GetTaskPushNotificationConfig":
		return "GetTaskPushNotificationConfig"
	case "ListTaskPushNotificationConfigs":
		return "ListTaskPushNotificationConfigs"
	case "DeleteTaskPushNotificationConfig":
		return "DeleteTaskPushNotificationConfig"
	case "GetExtendedAgentCard":
		return "GetExtendedAgentCard"
	default:
		return ""
	}
}

// ClassifyRequestPolicies decodes params through the same strict schemas used
// by the server and produces every policy requirement used by protocol
// middleware. A SendMessage carrying an inline push configuration deliberately
// requires both the message action and the risky push action.
func ClassifyRequestPolicies(request JSONRPCRequest) ([]RequestPolicy, error) {
	method := CanonicalMethod(request.Method)
	if method == "" {
		return nil, methodNotFoundError{method: request.Method}
	}
	policy := RequestPolicy{
		CanonicalMethod: method,
		Action:          "a2a." + method,
		ResourceID:      "*",
	}
	policies := []RequestPolicy{policy}
	switch method {
	case "SendMessage", "SendStreamingMessage":
		var params SendMessageParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.Message.TaskID)
		policy.Write = true
		policy.MessageID = strings.TrimSpace(params.Message.MessageID)
		policies[0] = policy
		if params.Configuration.TaskPushNotification != nil {
			policies = append(policies, RequestPolicy{
				CanonicalMethod: method,
				Action:          "a2a.push.configure",
				ResourceID:      policy.ResourceID,
				Write:           true,
				Risky:           true,
			})
		}
	case "GetTask":
		var params GetTaskParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.ID)
		policies[0] = policy
	case "ListTasks":
		var params ListTasksParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
	case "CancelTask", "SubscribeToTask":
		var params TaskIDParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.ID)
		policy.Write = method == "CancelTask"
		policies[0] = policy
	case "CreateTaskPushNotificationConfig":
		var params PushNotificationConfig
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.TaskID)
		policy.Write = true
		policies[0] = policy
		policies = append(policies, RequestPolicy{
			CanonicalMethod: method,
			Action:          "a2a.push.configure",
			ResourceID:      policy.ResourceID,
			Write:           true,
			Risky:           true,
		})
	case "GetTaskPushNotificationConfig", "DeleteTaskPushNotificationConfig":
		var params GetPushConfigParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.TaskID)
		policy.Write = method == "DeleteTaskPushNotificationConfig"
		policies[0] = policy
	case "ListTaskPushNotificationConfigs":
		var params ListPushConfigsParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		policy.ResourceID = policyResourceID(params.TaskID)
		policies[0] = policy
	case "GetExtendedAgentCard":
		var params GetExtendedAgentCardParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
	default:
		return nil, methodNotFoundError{method: request.Method}
	}
	return policies, nil
}

// CanonicalRequestPolicyPayload returns a stable semantic payload for loop
// detection. JSON object key order cannot be used to create a different digest
// for the same request.
func CanonicalRequestPolicyPayload(request JSONRPCRequest) ([]byte, error) {
	policies, err := ClassifyRequestPolicies(request)
	if err != nil {
		return nil, err
	}
	params, err := canonicalRequestParams(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{
		Method: policies[0].CanonicalMethod,
		Params: params,
	})
}

func canonicalRequestParams(request JSONRPCRequest) (any, error) {
	var target any
	switch CanonicalMethod(request.Method) {
	case "SendMessage", "SendStreamingMessage":
		target = &SendMessageParams{}
	case "GetTask":
		target = &GetTaskParams{}
	case "ListTasks":
		target = &ListTasksParams{}
	case "CancelTask", "SubscribeToTask":
		target = &TaskIDParams{}
	case "CreateTaskPushNotificationConfig":
		target = &PushNotificationConfig{}
	case "GetTaskPushNotificationConfig", "DeleteTaskPushNotificationConfig":
		target = &GetPushConfigParams{}
	case "ListTaskPushNotificationConfigs":
		target = &ListPushConfigsParams{}
	case "GetExtendedAgentCard":
		target = &GetExtendedAgentCardParams{}
	default:
		return nil, methodNotFoundError{method: request.Method}
	}
	if err := decodeParams(request.Params, target); err != nil {
		return nil, err
	}
	return reflect.ValueOf(target).Elem().Interface(), nil
}

func policyResourceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "*"
	}
	return value
}

func decodeExactJSON(raw []byte, target any) error {
	targetType := reflect.TypeOf(target)
	targetValue := reflect.ValueOf(target)
	if targetType == nil || targetType.Kind() != reflect.Pointer || targetValue.IsNil() {
		return errors.New("JSON decode target must be a non-nil pointer")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	if err := validateCanonicalJSONFields(raw, targetType.Elem(), "$"); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSONValue(decoder, "$"); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key at %s", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object at %s", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := walkJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array at %s", path)
		}
	default:
		return fmt.Errorf("invalid JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}

func validateCanonicalJSONFields(raw []byte, targetType reflect.Type, path string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return io.EOF
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	if isOpaqueJSONType(targetType) {
		return nil
	}

	switch targetType.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		fields := exactJSONFields(targetType)
		for key, value := range object {
			fieldType, ok := fields[key]
			if !ok {
				if nonCanonicalFieldName(key, fields) ||
					forbiddenLegacyField(targetType, key) {
					return fmt.Errorf(
						"non-canonical field %q at %s",
						key,
						path,
					)
				}
				// A2A 1.0 section 5.7 recommends ignoring unrecognized fields
				// for forward compatibility. They are discarded by the typed
				// decoder and never enter semantic policy/idempotency digests.
				continue
			}
			if err := validateCanonicalJSONFields(value, fieldType, path+"."+key); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if targetType.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for index, value := range values {
			if err := validateCanonicalJSONFields(
				value,
				targetType.Elem(),
				fmt.Sprintf("%s[%d]", path, index),
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func nonCanonicalFieldName(key string, fields map[string]reflect.Type) bool {
	for canonical := range fields {
		if strings.EqualFold(key, canonical) {
			return true
		}
	}
	return false
}

func forbiddenLegacyField(targetType reflect.Type, key string) bool {
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	switch targetType {
	case reflect.TypeOf(SendMessageParams{}):
		return strings.EqualFold(key, "linkedTicketId")
	case reflect.TypeOf(PushNotificationConfig{}):
		return strings.EqualFold(key, "config")
	default:
		return false
	}
}

func exactJSONFields(targetType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for index := 0; index < targetType.NumField(); index++ {
		field := targetType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embeddedType := field.Type
			for embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				for embeddedName, embeddedFieldType := range exactJSONFields(embeddedType) {
					fields[embeddedName] = embeddedFieldType
				}
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func isOpaqueJSONType(targetType reflect.Type) bool {
	if targetType == reflect.TypeOf(json.RawMessage{}) {
		return true
	}
	if targetType.Implements(jsonUnmarshalerType) {
		return true
	}
	if targetType.Kind() != reflect.Pointer &&
		reflect.PointerTo(targetType).Implements(jsonUnmarshalerType) {
		return true
	}
	switch targetType.Kind() {
	case reflect.Interface, reflect.Map:
		return true
	default:
		return false
	}
}

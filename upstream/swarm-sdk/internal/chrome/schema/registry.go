// Package schema publishes and validates the canonical model-facing Chrome tool schemas.
package schema

import (
	"encoding/json"
	"fmt"
	"sort"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"

// ToolContract pairs the input and successful JSON result schemas for one v1 tool.
type ToolContract struct {
	Input  map[string]any
	Result map[string]any
}

// ChromeContractSchemaRegistryV1 is the fail-closed canonical schema surface.
// Call Registry to obtain a deep copy.
var ChromeContractSchemaRegistryV1 = buildRegistry()

// Registry returns a deep copy so callers cannot mutate the canonical registry.
func Registry() map[string]ToolContract {
	data, _ := json.Marshal(ChromeContractSchemaRegistryV1)
	var copy map[string]ToolContract
	_ = json.Unmarshal(data, &copy)
	return copy
}

// ToolNames returns the complete sorted v1 tool surface.
func ToolNames() []string {
	names := make([]string, 0, len(ChromeContractSchemaRegistryV1))
	for name := range ChromeContractSchemaRegistryV1 {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup fails closed for unknown or omitted tools.
func Lookup(name string) (ToolContract, error) {
	contract, ok := ChromeContractSchemaRegistryV1[name]
	if !ok {
		return ToolContract{}, fmt.Errorf("chrome schema: no v1 contract for %q", name)
	}
	return contract, nil
}

func buildRegistry() map[string]ToolContract {
	registry := map[string]ToolContract{}
	add := func(name string, input, result map[string]any) {
		input["$schema"] = draft202012
		input["$id"] = "https://swarm.dev/schemas/chrome/v1/" + name + ".input.schema.json"
		result["$schema"] = draft202012
		result["$id"] = "https://swarm.dev/schemas/chrome/v1/" + name + ".result.schema.json"
		if _, isUnion := result["oneOf"]; !isUnion {
			result = withBrowser(result)
		}
		registry[name] = ToolContract{Input: input, Result: result}
	}

	add("chrome_session_status",
		object(props{"ensure_open": booleanDefault(false)}),
		objectRequired(props{
			"state":      enum("launching", "ready", "active", "closing", "closed", "failed"),
			"generation": epoch(), "tab_count": integerMin(0), "active_tab": nullable(handle()),
			"idle_for_ms": duration(), "closes_at": nullable(timestamp()), "in_flight": integerMin(0),
			"last_error": nullable(structuredError()),
		}, "state", "generation", "tab_count", "idle_for_ms", "in_flight"))
	add("chrome_tabs",
		object(props{"include_urls": booleanDefault(true)}),
		objectRequired(props{"tabs": array(tab())}, "tabs"))
	add("chrome_new_tab",
		object(props{"url": withDefault(stringMax(16384), "about:blank"), "active": booleanDefault(true)}),
		objectRequired(props{"tab": tab()}, "tab"))
	add("chrome_navigate",
		objectRequired(props{
			"tab": handle(), "target": oneOf(
				objectRequired(props{"url": stringMax(16384)}, "url"),
				objectRequired(props{"history": enum("back", "forward", "reload")}, "history"),
			),
			"wait_until": withDefault(enum("none", "domcontentloaded", "load"), "domcontentloaded"),
			"timeout_ms": withDefault(integerRange(0, 60000), 15000),
		}, "tab", "target"),
		objectRequired(props{
			"tab": handle(), "url": stringMax(16384), "title": stringSchema(),
			"document_epoch": epoch(), "load_state": enum("none", "loading", "domcontentloaded", "load"),
			"timed_out": boolean(),
		}, "tab", "url", "title", "document_epoch", "load_state", "timed_out"))
	add("chrome_read_page",
		objectRequired(props{
			"tab": handle(), "filter": withDefault(enum("all", "interactive"), "all"),
			"depth": withDefault(integerRange(1, 50), 15), "root_ref": handle(),
			"max_chars": withDefault(integerRange(1000, 200000), 50000),
		}, "tab"),
		objectRequired(props{
			"tab": handle(), "document_epoch": epoch(), "tree": stringSchema(),
			"truncated": boolean(), "next_hint": nullable(stringSchema()),
		}, "tab", "document_epoch", "tree", "truncated"))
	add("chrome_get_page_text",
		objectRequired(props{
			"tab": handle(), "mode": withDefault(enum("main", "visible", "full"), "main"),
			"max_chars": withDefault(integerRange(1000, 200000), 50000),
		}, "tab"),
		objectRequired(props{
			"title": stringSchema(), "url": stringMax(16384), "text": stringSchema(),
			"source": enum("main", "visible", "full"), "truncated": boolean(), "document_epoch": epoch(),
		}, "title", "url", "text", "source", "truncated", "document_epoch"))
	add("chrome_find",
		objectRequired(props{
			"tab": handle(), "query": stringRange(1, 1000),
			"limit": withDefault(integerRange(1, 100), 20),
		}, "tab", "query"),
		objectRequired(props{"matches": array(elementMatch()), "more": boolean()}, "matches", "more"))
	add("chrome_form_input",
		objectRequired(props{
			"tab": handle(), "ref": handle(),
			"value":    map[string]any{"type": []any{"string", "number", "boolean"}},
			"dispatch": withDefault(arrayItems(enum("input", "change", "blur")), []any{"input", "change"}),
		}, "tab", "ref", "value"),
		objectRequired(props{
			"ref": handle(), "control_type": stringSchema(), "value_set": boolean(),
			"checked": nullable(boolean()), "events_dispatched": arrayItems(stringSchema()),
		}, "ref", "control_type", "value_set", "events_dispatched"))
	add("chrome_computer", computerInput(), computerResult())
	add("chrome_javascript",
		objectRequired(props{
			"tab": handle(), "expression": stringRange(1, 100000),
			"await_promise": booleanDefault(true), "return_by_value": booleanDefault(true),
			"timeout_ms": withDefault(integerRange(1, 30000), 10000),
		}, "tab", "expression"),
		objectRequired(props{
			"value": map[string]any{}, "type": stringSchema(), "description": nullable(stringSchema()),
			"exception": nullable(javaScriptException()), "document_epoch": epoch(),
		}, "type", "document_epoch"))
	add("chrome_console",
		objectRequired(props{
			"tab": handle(), "pattern": stringMax(1000),
			"levels": arrayItems(enum("log", "info", "warn", "error", "exception")),
			"limit":  withDefault(integerRange(1, 1000), 100), "after_sequence": integerMin(0),
			"clear": booleanDefault(false),
		}, "tab"),
		objectRequired(props{
			"messages": array(consoleMessage()), "next_sequence": integerMin(0), "gap": boolean(),
		}, "messages", "next_sequence", "gap"))
	add("chrome_network",
		objectRequired(props{
			"tab": handle(), "url_contains": stringMax(2000), "resource_types": arrayItems(stringSchema()),
			"limit": withDefault(integerRange(1, 1000), 100), "after_sequence": integerMin(0),
			"clear": booleanDefault(false),
		}, "tab"),
		objectRequired(props{
			"requests": array(networkRequest()), "next_sequence": integerMin(0), "gap": boolean(),
		}, "requests", "next_sequence", "gap"))
	add("chrome_resize_window",
		objectRequired(props{"width": integerRange(320, 7680), "height": integerRange(240, 4320)}, "width", "height"),
		objectRequired(props{
			"width": integer(), "height": integer(), "device_scale_factor": numberExclusiveMin(0),
		}, "width", "height", "device_scale_factor"))
	add("chrome_upload",
		objectRequired(props{
			"tab": handle(),
			"source": oneOf(
				objectRequired(props{"image_handle": handle()}, "image_handle"),
				objectRequired(props{"file_path": stringSchema()}, "file_path"),
			),
			"target": oneOf(
				objectRequired(props{"ref": handle()}, "ref"),
				objectRequired(props{"coordinate": coordinate()}, "coordinate"),
			),
			"filename": stringMax(255),
		}, "tab", "source", "target"),
		objectRequired(props{
			"uploaded": map[string]any{"const": true}, "filename": stringSchema(), "bytes": integerMin(0),
			"method": enum("file_input", "drag_drop"),
		}, "uploaded", "filename", "bytes", "method"))
	add("chrome_close_session",
		object(props{"reason": stringMax(500)}),
		objectRequired(props{"closed": boolean(), "generation": epoch()}, "closed", "generation"))
	return registry
}

type props map[string]any

func object(properties props) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any(properties), "additionalProperties": false}
}

func objectRequired(properties props, required ...string) map[string]any {
	result := object(properties)
	result["required"] = stringsToAny(required)
	return result
}

func withBrowser(result map[string]any) map[string]any {
	properties := result["properties"].(map[string]any)
	properties["browser"] = browserMetadata()
	required, _ := result["required"].([]any)
	result["required"] = append(required, "browser")
	return result
}

func browserMetadata() map[string]any {
	return objectRequired(props{
		"generation": epoch(), "tab_handle": handle(), "document_epoch": epoch(),
		"request_id": requestID(), "started_at": timestamp(), "duration_ms": duration(),
		"activity_deadline": timestamp(),
	}, "generation", "request_id", "started_at", "duration_ms", "activity_deadline")
}

func tab() map[string]any {
	return objectRequired(props{
		"handle": handle(), "title": stringSchema(), "url": stringMax(16384),
		"active": boolean(), "document_epoch": epoch(),
	}, "handle", "title", "url", "active", "document_epoch")
}

func bounds() map[string]any {
	return objectRequired(props{"x": number(), "y": number(), "width": number(), "height": number()}, "x", "y", "width", "height")
}

func elementMatch() map[string]any {
	return objectRequired(props{
		"ref": handle(), "role": stringSchema(), "name": stringSchema(), "bounds": bounds(),
		"visible": boolean(), "enabled": boolean(),
	}, "ref", "role", "name", "bounds", "visible", "enabled")
}

func consoleMessage() map[string]any {
	return objectRequired(props{
		"sequence": integerMin(0), "level": enum("log", "info", "warn", "error", "exception"),
		"text": stringSchema(), "url": stringMax(16384), "timestamp": timestamp(),
	}, "sequence", "level", "text", "url", "timestamp")
}

func networkRequest() map[string]any {
	return objectRequired(props{
		"sequence": integerMin(0), "method": stringSchema(), "url": stringMax(16384),
		"status": nullable(integer()), "mime_type": nullable(stringSchema()),
		"resource_type": stringSchema(), "timestamp": timestamp(),
	}, "sequence", "method", "url", "status", "mime_type", "resource_type", "timestamp")
}

func javaScriptException() map[string]any {
	return objectRequired(props{
		"text": stringSchema(), "line": integerMin(0), "column": integerMin(0), "stack": stringMax(100000),
	}, "text", "line", "column")
}

func structuredError() map[string]any {
	return objectRequired(props{
		"code": stringSchema(), "message": stringSchema(), "retryable": boolean(),
		"execution": enum("not_started", "failed", "indeterminate"), "request_id": requestID(),
		"generation": epoch(), "details": object(props{
			"reason": stringMax(1000), "missing_capability": stringMax(255),
		}),
	}, "code", "message", "retryable", "execution", "details")
}

func computerInput() map[string]any {
	common := props{
		"tab": handle(), "action": enum(
			"left_click", "right_click", "double_click", "triple_click", "type", "key",
			"hover", "scroll", "drag", "screenshot", "zoom", "scroll_to", "wait",
		),
		"coordinate": coordinate(), "start_coordinate": coordinate(), "region": numberTuple(4),
		"ref": handle(), "text": stringSchema(),
		"modifiers": uniqueArray(enum("ctrl", "shift", "alt", "meta")),
		"direction": enum("up", "down", "left", "right"), "amount": number(),
		"duration_ms": integerRange(0, 30000), "repeat": withDefault(integerRange(1, 100), 1),
	}
	var variants []any
	for _, action := range []string{"left_click", "right_click", "double_click", "triple_click"} {
		variants = append(variants,
			actionObject(common, action, []string{"tab", "action", "coordinate"}, "coordinate", "modifiers"),
			actionObject(common, action, []string{"tab", "action", "ref"}, "ref", "modifiers"))
	}
	for _, action := range []string{"hover"} {
		variants = append(variants,
			actionObject(common, action, []string{"tab", "action", "coordinate"}, "coordinate"),
			actionObject(common, action, []string{"tab", "action", "ref"}, "ref"))
	}
	variants = append(variants,
		actionObject(common, "type", []string{"tab", "action", "text"}, "text"),
		actionObject(common, "key", []string{"tab", "action", "text"}, "text", "repeat"),
		actionObject(common, "scroll", []string{"tab", "action", "direction"}, "direction", "coordinate", "amount"),
		actionObject(common, "drag", []string{"tab", "action", "start_coordinate", "coordinate"}, "start_coordinate", "coordinate", "duration_ms"),
		actionObject(common, "screenshot", []string{"tab", "action"}),
		actionObject(common, "zoom", []string{"tab", "action", "region"}, "region"),
		actionObject(common, "scroll_to", []string{"tab", "action", "ref"}, "ref"),
		actionObject(common, "wait", []string{"tab", "action", "duration_ms"}, "duration_ms"),
	)
	return map[string]any{"$schema": draft202012, "oneOf": variants}
}

func actionObject(common props, action string, required []string, allowed ...string) map[string]any {
	properties := props{"tab": common["tab"], "action": map[string]any{"const": action}}
	for _, name := range allowed {
		properties[name] = common[name]
	}
	return objectRequired(properties, required...)
}

func computerResult() map[string]any {
	with := func(properties props, required ...string) map[string]any {
		properties["browser"] = browserMetadata()
		required = append(required, "browser")
		return objectRequired(properties, required...)
	}
	var variants []any
	for _, action := range []string{"screenshot", "zoom"} {
		variants = append(variants, with(props{
			"action": map[string]any{"const": action}, "image_handle": handle(),
			"width": integerMin(1), "height": integerMin(1), "mime_type": enum("image/png", "image/jpeg"),
		}, "action", "image_handle", "width", "height", "mime_type"))
	}
	variants = append(variants,
		with(props{
			"action": map[string]any{"const": "wait"}, "waited_ms": duration(),
			"state": enum("ready", "closed", "cancelled"),
		}, "action", "waited_ms", "state"),
		with(props{
			"action": map[string]any{"const": "scroll_to"}, "ref": handle(),
			"performed": boolean(), "document_epoch": epoch(),
		}, "action", "ref", "performed", "document_epoch"),
	)
	for _, action := range []string{
		"left_click", "right_click", "double_click", "triple_click", "type", "key",
		"hover", "scroll", "drag",
	} {
		variants = append(variants, with(props{
			"action": map[string]any{"const": action}, "performed": boolean(), "document_epoch": epoch(),
		}, "action", "performed", "document_epoch"))
	}
	return map[string]any{"oneOf": variants}
}

func oneOf(values ...map[string]any) map[string]any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return map[string]any{"oneOf": result}
}

func nullable(value map[string]any) map[string]any {
	return oneOf(value, map[string]any{"type": "null"})
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}
func boolean() map[string]any   { return map[string]any{"type": "boolean"} }
func integer() map[string]any   { return map[string]any{"type": "integer"} }
func number() map[string]any    { return map[string]any{"type": "number"} }
func epoch() map[string]any     { return integerMin(1) }
func duration() map[string]any  { return integerMin(0) }
func timestamp() map[string]any { return map[string]any{"type": "string", "format": "date-time"} }
func handle() map[string]any {
	return map[string]any{"type": "string", "pattern": `^[a-z]+_[A-Za-z0-9_-]{16,}$`}
}
func requestID() map[string]any            { return map[string]any{"type": "string", "minLength": 1} }
func enum(values ...string) map[string]any { return map[string]any{"enum": stringsToAny(values)} }
func integerMin(minimum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum}
}
func numberExclusiveMin(minimum int) map[string]any {
	return map[string]any{"type": "number", "exclusiveMinimum": minimum}
}
func stringMax(maximum int) map[string]any {
	return map[string]any{"type": "string", "maxLength": maximum}
}
func stringRange(minimum, maximum int) map[string]any {
	return map[string]any{"type": "string", "minLength": minimum, "maxLength": maximum}
}
func integerRange(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}
func booleanDefault(value bool) map[string]any { return withDefault(boolean(), value) }
func withDefault(value map[string]any, defaultValue any) map[string]any {
	value["default"] = defaultValue
	return value
}
func array(item map[string]any) map[string]any      { return map[string]any{"type": "array", "items": item} }
func arrayItems(item map[string]any) map[string]any { return array(item) }
func uniqueArray(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item, "uniqueItems": true}
}
func coordinate() map[string]any { return numberTuple(2) }
func numberTuple(length int) map[string]any {
	prefix := make([]any, length)
	for index := range prefix {
		prefix[index] = number()
	}
	return map[string]any{
		"type": "array", "prefixItems": prefix, "items": false,
		"minItems": length, "maxItems": length,
	}
}
func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

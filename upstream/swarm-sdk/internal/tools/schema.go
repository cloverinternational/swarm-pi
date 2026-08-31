package tools

import (
	"reflect"
	"strings"
	"sync"
)

// schemaCache stores computed JSON Schema objects keyed on reflect.Type.
// SchemaFor[T]() is called once per tool per provider turn; caching avoids
// repeated reflect traversals for the same type.
var schemaCache sync.Map // key: reflect.Type, value: map[string]any

// SchemaFor generates a JSON Schema object for the struct type P.
// It is the companion to [TypedTool]: use it in your Parameters() method so
// the schema stays in sync with your params struct automatically.
//
// Supported struct tags:
//
//   - json:"name"          — sets the property name (honours omitempty; skips "-")
//   - description:"..."    — adds a "description" to the property
//   - required:"true"      — adds the field to the schema's "required" array
//   - enum:"a,b,c"         — adds an "enum" constraint to the property
//   - default:"value"      — adds a "default" hint to the property
//
// Supported Go types and their JSON Schema equivalents:
//
//	string                       → {"type":"string"}
//	int / int8 / int16 / int32 / int64 / uint* → {"type":"integer"}
//	float32 / float64            → {"type":"number"}
//	bool                         → {"type":"boolean"}
//	[]T                          → {"type":"array","items":<schema for T>}
//	map[string]V                 → {"type":"object"}
//	*T                           → same as T (pointer unwrapping)
//	struct                       → {"type":"object","properties":{...},"required":[...]}
//
// Example:
//
//	type SearchParams struct {
//	    Query      string `json:"query"       description:"Search query"   required:"true"`
//	    MaxResults int    `json:"max_results"  description:"Max results"                  `
//	    Format     string `json:"format"       description:"Output format"   enum:"json,text"`
//	}
//
//	func (t *SearchTool) Parameters() any {
//	    return tools.SchemaFor[SearchParams]()
//	}
func SchemaFor[P any]() map[string]any {
	var zero P
	t := reflect.TypeOf(zero)
	// Unwrap pointer: SchemaFor[*SearchParams] should work the same as SchemaFor[SearchParams]
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		// Non-struct types return a minimal schema — not worth caching.
		return schemaForType(reflect.TypeOf(zero))
	}
	// Return the cached schema if already computed for this type.
	if cached, ok := schemaCache.Load(t); ok {
		return cached.(map[string]any)
	}
	schema := schemaForStruct(t)
	schemaCache.Store(t, schema)
	return schema
}

// schemaForType returns the JSON Schema for an arbitrary reflect.Type.
func schemaForType(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	// Unwrap pointers
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice:
		schema := map[string]any{"type": "array"}
		if t.Elem() != nil {
			schema["items"] = schemaForType(t.Elem())
		}
		return schema
	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": schemaForType(t.Elem()),
		}
	case reflect.Struct:
		return schemaForStruct(t)
	default:
		return map[string]any{}
	}
}

// schemaForStruct builds a JSON Schema object from a struct type's fields.
func schemaForStruct(t reflect.Type) map[string]any {
	properties := map[string]any{}
	required := []string{}

	for field := range t.Fields() {
		field := field

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Resolve the JSON property name from the json tag
		name, omitEmpty := jsonFieldName(field)
		if name == "-" {
			continue // json:"-" means skip entirely
		}

		// Build the property schema from the field's type
		prop := schemaForType(field.Type)

		// description tag → property description
		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}

		// enum tag → enum constraint  ("a,b,c" → ["a","b","c"])
		if enumStr := field.Tag.Get("enum"); enumStr != "" {
			vals := strings.Split(enumStr, ",")
			enumVals := make([]any, 0, len(vals))
			for _, v := range vals {
				v = strings.TrimSpace(v)
				if v != "" {
					enumVals = append(enumVals, v)
				}
			}
			if len(enumVals) > 0 {
				prop["enum"] = enumVals
			}
		}

		// default tag → default hint
		if def := field.Tag.Get("default"); def != "" {
			prop["default"] = def
		}

		properties[name] = prop

		// required tag and lack of omitempty determine required fields.
		// A field is required if it is tagged required:"true"
		// (omitempty implies the field is optional and we respect that).
		isRequired := field.Tag.Get("required") == "true"
		if isRequired && !omitEmpty {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// jsonFieldName returns the JSON key name for a struct field, plus whether
// omitempty is set. If the json tag is absent, the field name is returned as-is.
func jsonFieldName(f reflect.StructField) (name string, omitEmpty bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, false
	}
	parts := strings.SplitN(tag, ",", 2)
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	if len(parts) > 1 {
		for opt := range strings.SplitSeq(parts[1], ",") {
			if strings.TrimSpace(opt) == "omitempty" {
				omitEmpty = true
			}
		}
	}
	return name, omitEmpty
}

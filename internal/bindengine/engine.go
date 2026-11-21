// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package bindengine is the reflection engine of MX Echo's binder. It binds request values, keyed by a struct
// tag, into the tagged fields of a struct, into a string-keyed map, or into multipart file headers.
//
// The root's Bind* functions and DefaultBinder call it with their request's values. It imports nothing of the
// root: the root's BindUnmarshaler is checked here structurally, as Unmarshaler.
package bindengine

import (
	"encoding"
	"errors"
	"fmt"
	"mime/multipart"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnsupportedMapValue is returned when Bind's destination is a string-keyed map whose values are neither
// strings, string slices nor any. The root's errUnsupportedBindMapValue is this value.
var ErrUnsupportedMapValue = errors.New("binding element must be a map with string, []string or any values")

// Unmarshaler has the method set of the root's BindUnmarshaler, so a type implementing one implements the
// other. Types that don't implement this, but do implement encoding.TextUnmarshaler will use that interface
// instead.
type Unmarshaler interface {
	// UnmarshalParam decodes and assigns a value from an form or query param.
	UnmarshalParam(param string) error
}

// MultipleUnmarshaler is used by binder to unmarshal multiple values from request at once to
// type implementing this interface. For example request could have multiple query fields `?a=1&a=2&b=test` in that case
// for `a` following slice `["1", "2"] will be passed to unmarshaller.
type MultipleUnmarshaler interface {
	UnmarshalParams(params []string) error
}

// bindFieldMeta is the cached, type-level reflection metadata for a single struct field. Reading struct
// tags (reflect.StructTag.Get) parses the tag string on every call, so for binding-heavy endpoints we
// compute it once per struct type and reuse it across requests (see bindStructMeta). Only type-level data
// is cached here; per-request, per-instance reflect.Value operations still happen in Bind.
type bindFieldMeta struct {
	index int // field index within the struct
	// fieldKind is the DECLARED field kind (typeField.Type.Kind()), used only for unmarshal dispatch.
	// It is intentionally not the post-anonymous-pointer-deref live kind; Bind computes that
	// separately as structFieldKind where needed.
	fieldKind reflect.Kind
	anonymous bool   // reflect.StructField.Anonymous
	formatTag string // value of the `format` struct tag
	// binding-source tag values. Bind is only ever called with one of these four tags (see the
	// root's callers BindPathValues/BindQueryParams/BindBody/BindHeaders). Keep these fields, the four
	// f.Tag.Get(...) lines in bindMetaFor, and the tagName switch in sync if a source is ever added.
	param, query, form, header string
}

// tagName returns the field's tag value for the given binding source tag.
// Keep in sync with the tag fields above and the f.Tag.Get calls in bindMetaFor.
func (m *bindFieldMeta) tagName(tag string) string {
	switch tag {
	case "param":
		return m.param
	case "query":
		return m.query
	case "form":
		return m.form
	case "header":
		return m.header
	default:
		return ""
	}
}

// bindStructMeta is the cached field metadata for a whole struct type, in declaration order.
type bindStructMeta struct {
	fields []bindFieldMeta
}

// bindStructCache memoizes bindStructMeta keyed by struct reflect.Type. Concurrent double-computation is
// harmless because the result is deterministic and idempotent.
var bindStructCache sync.Map // map[reflect.Type]*bindStructMeta

func bindMetaFor(typ reflect.Type) *bindStructMeta {
	if cached, ok := bindStructCache.Load(typ); ok {
		return cached.(*bindStructMeta)
	}
	n := typ.NumField()
	meta := &bindStructMeta{fields: make([]bindFieldMeta, n)}
	for i := range n {
		f := typ.Field(i)
		meta.fields[i] = bindFieldMeta{
			index:     i,
			anonymous: f.Anonymous,
			fieldKind: f.Type.Kind(),
			formatTag: f.Tag.Get("format"),
			param:     f.Tag.Get("param"),
			query:     f.Tag.Get("query"),
			form:      f.Tag.Get("form"),
			header:    f.Tag.Get("header"),
		}
	}
	bindStructCache.Store(typ, meta)
	return meta
}

// Bind will bind data ONLY fields in destination struct that have EXPLICIT tag
func Bind(destination any, data map[string][]string, tag string, dataFiles map[string][]*multipart.FileHeader) error {
	if destination == nil || (len(data) == 0 && len(dataFiles) == 0) {
		return nil
	}
	hasFiles := len(dataFiles) > 0
	typ := reflect.TypeOf(destination).Elem()
	val := reflect.ValueOf(destination).Elem()

	if typ.Kind() == reflect.Map && typ.Key().Kind() == reflect.String {
		return bindStringKeyedMap(typ, val, data)
	}

	if typ.Kind() != reflect.Struct {
		if tag == "param" || tag == "query" || tag == "header" {
			// incompatible type, data is probably to be found in the body
			return nil
		}
		return errors.New("binding element must be a struct")
	}

	meta := bindMetaFor(typ)
	for fi := range meta.fields { // iterate over all destination fields
		fm := &meta.fields[fi]
		structField := val.Field(fm.index)
		if fm.anonymous {
			if structField.Kind() == reflect.Pointer {
				structField = structField.Elem()
			}
		}
		if !structField.CanSet() {
			continue
		}
		structFieldKind := structField.Kind()
		inputFieldName := fm.tagName(tag)
		if fm.anonymous && structFieldKind == reflect.Struct && inputFieldName != "" {
			// if anonymous struct with query/param/form tags, report an error
			return errors.New("query/param/form tags are not allowed with anonymous struct field")
		}

		if inputFieldName == "" {
			// If tag is nil, we inspect if the field is a not Unmarshaler struct and try to bind data into it (might contain fields with tags).
			// structs that implement Unmarshaler are bound only when they have explicit tag
			if _, ok := structField.Addr().Interface().(Unmarshaler); !ok && structFieldKind == reflect.Struct {
				if err := Bind(structField.Addr().Interface(), data, tag, dataFiles); err != nil {
					return err
				}
			}
			// does not have explicit tag and is not an ordinary struct - so move to next field
			continue
		}

		if hasFiles {
			if ok, err := isFieldMultipartFile(structField.Type()); err != nil {
				return err
			} else if ok {
				if ok := setMultipartFileHeaderTypes(structField, inputFieldName, dataFiles); ok {
					continue
				}
			}
		}

		inputValue, exists := data[inputFieldName]
		if !exists {
			// Go json.Unmarshal supports case-insensitive binding.  However the
			// url params are bound case-sensitive which is inconsistent.  To
			// fix this we must check all of the map values in a
			// case-insensitive search.
			for k, v := range data {
				if strings.EqualFold(k, inputFieldName) {
					inputValue = v
					exists = true
					break
				}
			}
		}

		if !exists {
			continue
		}
		if len(inputValue) == 0 {
			// Preserve custom multi-value unmarshalling, but otherwise treat
			// entries without values as absent (without allocating pointers).
			value := structField
			if structFieldKind != reflect.Pointer {
				value = structField.Addr()
			}
			if _, multiple := value.Interface().(MultipleUnmarshaler); !multiple {
				continue
			}
		}

		// NOTE: algorithm here is not particularly sophisticated. It probably does not work with absurd types like `**[]*int`
		// but it is smart enough to handle niche cases like `*int`,`*[]string`,`[]*int` .

		// try unmarshalling first, in case we're dealing with an alias to an array type
		if ok, err := unmarshalInputsToField(fm.fieldKind, inputValue, structField); ok {
			if err != nil {
				return fmt.Errorf("%s: %w", inputFieldName, err)
			}
			continue
		}

		if ok, err := unmarshalInputToField(fm.fieldKind, inputValue[0], structField, fm.formatTag); ok {
			if err != nil {
				return fmt.Errorf("%s: %w", inputFieldName, err)
			}
			continue
		}

		// we could be dealing with pointer to slice `*[]string` so dereference it. There are weird OpenAPI generators
		// that could create struct fields like that.
		if structFieldKind == reflect.Pointer {
			structFieldKind = structField.Elem().Kind()
			structField = structField.Elem()
		}

		if structFieldKind == reflect.Slice {
			sliceOf := structField.Type().Elem().Kind()
			numElems := len(inputValue)
			slice := reflect.MakeSlice(structField.Type(), numElems, numElems)
			for j := range numElems {
				if err := SetWithProperType(sliceOf, inputValue[j], slice.Index(j)); err != nil {
					return fmt.Errorf("%s: %w", inputFieldName, err)
				}
			}
			structField.Set(slice)
			continue
		}

		if err := SetWithProperType(structFieldKind, inputValue[0], structField); err != nil {
			return fmt.Errorf("%s: %w", inputFieldName, err)
		}
	}
	return nil
}

// bindStringKeyedMap binds repeated values to string slices and the first value to
// string or empty-interface elements. Check assignability before modifying the map.
func bindStringKeyedMap(typ reflect.Type, val reflect.Value, data map[string][]string) error {
	keyType, elemType := typ.Key(), typ.Elem()
	stringType := reflect.TypeFor[string]()
	bindsSlice := elemType.Kind() == reflect.Slice && reflect.TypeFor[[]string]().AssignableTo(elemType)
	if !bindsSlice && !stringType.AssignableTo(elemType) {
		return ErrUnsupportedMapValue
	}
	if val.IsNil() {
		val.Set(reflect.MakeMap(typ))
	}
	for k, v := range data {
		// A scalar destination needs a value. Slice destinations can retain
		// empty slices, which is their existing binding behavior.
		if len(v) == 0 && !bindsSlice {
			continue
		}
		key := reflect.ValueOf(k)
		if keyType != stringType {
			key = key.Convert(keyType)
		}
		if bindsSlice {
			val.SetMapIndex(key, reflect.ValueOf(v))
		} else {
			// map[string]any also receives only the first value.
			val.SetMapIndex(key, reflect.ValueOf(v[0]))
		}
	}
	return nil
}

// SetWithProperType sets structField from val as valueKind says: through an Unmarshaler or an
// encoding.TextUnmarshaler first, then as an integer, unsigned integer, bool, float or string.
func SetWithProperType(valueKind reflect.Kind, val string, structField reflect.Value) error {
	// But also call it here, in case we're dealing with an array of Unmarshalers
	// Note: format tag not available in this context, so empty string is passed
	if ok, err := unmarshalInputToField(valueKind, val, structField, ""); ok {
		return err
	}

	switch valueKind {
	case reflect.Pointer:
		return SetWithProperType(structField.Elem().Kind(), val, structField.Elem())
	case reflect.Int:
		return setIntField(val, 0, structField)
	case reflect.Int8:
		return setIntField(val, 8, structField)
	case reflect.Int16:
		return setIntField(val, 16, structField)
	case reflect.Int32:
		return setIntField(val, 32, structField)
	case reflect.Int64:
		return setIntField(val, 64, structField)
	case reflect.Uint:
		return setUintField(val, 0, structField)
	case reflect.Uint8:
		return setUintField(val, 8, structField)
	case reflect.Uint16:
		return setUintField(val, 16, structField)
	case reflect.Uint32:
		return setUintField(val, 32, structField)
	case reflect.Uint64:
		return setUintField(val, 64, structField)
	case reflect.Bool:
		return setBoolField(val, structField)
	case reflect.Float32:
		return setFloatField(val, 32, structField)
	case reflect.Float64:
		return setFloatField(val, 64, structField)
	case reflect.String:
		structField.SetString(val)
	default:
		return errors.New("unknown type")
	}
	return nil
}

func unmarshalInputsToField(valueKind reflect.Kind, values []string, field reflect.Value) (bool, error) {
	if valueKind == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	fieldIValue := field.Addr().Interface()
	unmarshaler, ok := fieldIValue.(MultipleUnmarshaler)
	if !ok {
		return false, nil
	}
	return true, unmarshaler.UnmarshalParams(values)
}

func unmarshalInputToField(valueKind reflect.Kind, val string, field reflect.Value, formatTag string) (bool, error) {
	if valueKind == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	fieldIValue := field.Addr().Interface()
	// date-time uses the same TextUnmarshaler as an untagged time.Time.
	if formatTag != "" && formatTag != "date-time" {
		if _, isTime := fieldIValue.(*time.Time); isTime {
			// OpenAPI dates and local date-times have no timezone. time.Parse assigns UTC,
			// as it does for custom layouts without timezone information.
			layout := formatTag
			switch formatTag {
			case "date":
				layout = "2006-01-02"
			case "date-time-local":
				layout = "2006-01-02T15:04:05"
			}
			t, err := time.Parse(layout, val)
			if err != nil {
				return true, err
			}
			field.Set(reflect.ValueOf(t))
			return true, nil
		}
	}

	switch unmarshaler := fieldIValue.(type) {
	case Unmarshaler:
		return true, unmarshaler.UnmarshalParam(val)
	case encoding.TextUnmarshaler:
		return true, unmarshaler.UnmarshalText([]byte(val))
	}

	return false, nil
}

func setIntField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0"
	}
	intVal, err := strconv.ParseInt(value, 10, bitSize)
	if err == nil {
		field.SetInt(intVal)
	}
	return err
}

func setUintField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0"
	}
	uintVal, err := strconv.ParseUint(value, 10, bitSize)
	if err == nil {
		field.SetUint(uintVal)
	}
	return err
}

func setBoolField(value string, field reflect.Value) error {
	if value == "" {
		value = "false"
	}
	boolVal, err := strconv.ParseBool(value)
	if err == nil {
		field.SetBool(boolVal)
	}
	return err
}

func setFloatField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0.0"
	}
	floatVal, err := strconv.ParseFloat(value, bitSize)
	if err == nil {
		field.SetFloat(floatVal)
	}
	return err
}

var (
	// NOT supported by bind as you can NOT check easily empty struct being actual file or not
	multipartFileHeaderType = reflect.TypeFor[multipart.FileHeader]()
	// supported by bind as you can check by nil value if file existed or not
	multipartFileHeaderPointerType      = reflect.TypeFor[*multipart.FileHeader]()
	multipartFileHeaderSliceType        = reflect.TypeFor[[]multipart.FileHeader]()
	multipartFileHeaderPointerSliceType = reflect.TypeFor[[]*multipart.FileHeader]()
)

func isFieldMultipartFile(field reflect.Type) (bool, error) {
	switch field {
	case multipartFileHeaderPointerType,
		multipartFileHeaderSliceType,
		multipartFileHeaderPointerSliceType:
		return true, nil
	case multipartFileHeaderType:
		return true, errors.New("binding to multipart.FileHeader struct is not supported, use pointer to struct")
	default:
		return false, nil
	}
}

func setMultipartFileHeaderTypes(structField reflect.Value, inputFieldName string, files map[string][]*multipart.FileHeader) bool {
	fileHeaders := files[inputFieldName]
	if len(fileHeaders) == 0 {
		return false
	}

	result := true
	switch structField.Type() {
	case multipartFileHeaderPointerSliceType:
		structField.Set(reflect.ValueOf(fileHeaders))
	case multipartFileHeaderSliceType:
		headers := make([]multipart.FileHeader, len(fileHeaders))
		for i, fileHeader := range fileHeaders {
			headers[i] = *fileHeader
		}
		structField.Set(reflect.ValueOf(headers))
	case multipartFileHeaderPointerType:
		structField.Set(reflect.ValueOf(fileHeaders[0]))
	default:
		result = false
	}

	return result
}

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// decodeStrictJSON keeps published human request schemas and runtime DTOs in
// lockstep: unknown fields, empty bodies, trailing JSON values, and binding
// validation failures are all rejected before a service mutation can run.
func decodeStrictJSON(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// decodeStrictJSONObject additionally reports which top-level properties were
// present. Pointer DTO fields alone cannot distinguish an omitted property
// from an explicit JSON null, but closed Human contracts sometimes need that
// distinction before a domain mutation is authorized.
func decodeStrictJSONObject(
	c *gin.Context,
	target any,
) (map[string]json.RawMessage, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, errors.New("request body is required")
	}
	var raw bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(c.Request.Body, &raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("request body must contain one JSON value")
		}
		return nil, err
	}
	if err := binding.Validator.ValidateStruct(target); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw.Bytes(), &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("request body must contain one JSON object")
	}
	canonicalFields, err := canonicalJSONFieldNames(target)
	if err != nil {
		return nil, err
	}
	for field := range fields {
		if _, ok := canonicalFields[field]; !ok {
			return nil, errors.New("request body contains a non-canonical field")
		}
	}
	return fields, nil
}

func canonicalJSONFieldNames(target any) (map[string]struct{}, error) {
	targetType := reflect.TypeOf(target)
	for targetType != nil && targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	if targetType == nil || targetType.Kind() != reflect.Struct {
		return nil, errors.New("strict JSON target must be a struct")
	}

	fields := make(map[string]struct{}, targetType.NumField())
	for index := 0; index < targetType.NumField(); index++ {
		field := targetType.Field(index)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}
		tagName := strings.Split(field.Tag.Get("json"), ",")[0]
		if tagName == "-" {
			continue
		}
		if tagName != "" {
			fields[tagName] = struct{}{}
			continue
		}
		if field.Anonymous {
			embedded, err := canonicalJSONFieldNames(
				reflect.New(field.Type).Interface(),
			)
			if err != nil {
				return nil, err
			}
			for name := range embedded {
				fields[name] = struct{}{}
			}
			continue
		}
		fields[field.Name] = struct{}{}
	}
	return fields, nil
}

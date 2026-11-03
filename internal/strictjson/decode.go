// Package strictjson decodes bounded, single-object JSON request bodies.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const maximumRequestBytes = 1 << 20

// Decode accepts exactly one small JSON object, rejects duplicate and unknown
// fields, and never logs its contents.
func Decode(reader io.Reader, destination any) error {
	contents, err := io.ReadAll(io.LimitReader(reader, maximumRequestBytes+1))
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	if len(contents) > maximumRequestBytes {
		return errors.New("request exceeds 1 MiB")
	}
	if !utf8.Valid(contents) {
		return errors.New("request must contain valid UTF-8")
	}
	if err = validateUniqueObject(contents); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	return nil
}

func validateUniqueObject(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := requireObject(decoder); err != nil {
		return err
	}
	if err := scanObject(decoder, "", 0); err != nil {
		return err
	}
	if err := finishInput(decoder); err != nil {
		return err
	}
	return nil
}

func requireObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("request must be one JSON object")
	}
	return nil
}

func scanObject(decoder *json.Decoder, field string, depth int) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		name, err := decodeFieldName(decoder, seen)
		if err != nil {
			return err
		}
		if err = scanValue(decoder, name, depth); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode request field %q: %w", field, err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("request object is not closed")
	}
	return nil
}

func decodeFieldName(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", fmt.Errorf("decode request: %w", err)
	}
	field, valid := token.(string)
	if !valid {
		return "", errors.New("request field name must be a string")
	}
	if _, duplicate := seen[field]; duplicate {
		return "", fmt.Errorf("duplicate request field %q", field)
	}
	seen[field] = struct{}{}
	return field, nil
}

// scanValue extends the duplicate ban to every nested object and array.
// encoding/json keeps the last duplicate, so scan the bounded body once before
// its typed decode. The root object is excluded from the 100 nested levels.
func scanValue(decoder *json.Decoder, field string, depth int) error {
	if depth > 100 {
		return fmt.Errorf("decode request field %q: request is nested too deeply", field)
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode request field %q: %w", field, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanObject(decoder, field, depth+1)
	case '[':
		for decoder.More() {
			if err = scanValue(decoder, field, depth+1); err != nil {
				return err
			}
		}
		closeToken, closeErr := decoder.Token()
		if closeErr != nil {
			return fmt.Errorf("decode request field %q: %w", field, closeErr)
		}
		if closing, ok := closeToken.(json.Delim); !ok || closing != ']' {
			return fmt.Errorf("decode request field %q: array is not closed", field)
		}
	}
	return nil
}

func finishInput(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	return errors.New("request must contain exactly one JSON object")
}

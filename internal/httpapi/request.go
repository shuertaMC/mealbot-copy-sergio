package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// DefaultMaxRequestBytes is the legacy 32 MiB request ceiling used when a
// handler constructor receives no positive configured limit.
const DefaultMaxRequestBytes int64 = 32 << 20

// QueryValue returns the required query parameter after enforcing exactly one
// non-blank value. Its validation errors are safe to map to HTTP 400.
func QueryValue(request *http.Request, key string) (string, error) {
	if request == nil {
		return "", domain.NewValidationError("query", "request is required")
	}
	values, exists := request.URL.Query()[key]
	if !exists || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", domain.NewValidationError("query."+key, "must contain exactly one non-empty value")
	}
	return strings.TrimSpace(values[0]), nil
}

// QueryValues returns required query values in key order. Every key must have
// exactly one non-blank value.
func QueryValues(request *http.Request, keys ...string) ([]string, error) {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		value, err := QueryValue(request, key)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func maxRequestBytes(configured int64) int64 {
	if configured <= 0 {
		return DefaultMaxRequestBytes
	}
	return configured
}

func decodeJSONBody(request *http.Request, limit int64, destination any) error {
	if request.Body == nil {
		return domain.NewValidationError("body", "must not be empty")
	}
	limit = maxRequestBytes(limit)
	contents, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		return domain.NewValidationError("body", "could not be read")
	}
	if int64(len(contents)) > limit {
		return domain.NewValidationError("body", "exceeds the request size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return domain.NewValidationError("body", "must contain valid JSON")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return domain.NewValidationError("body", "must contain one JSON value")
		}
		return domain.NewValidationError("body", "contains malformed trailing data")
	}
	return nil
}

func requestPart(request *http.Request, field string, limit int64) ([]byte, error) {
	limit = maxRequestBytes(limit)
	request.Body = http.MaxBytesReader(nil, request.Body, limit)
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, domain.NewValidationError("multipart", "must be valid multipart form data")
	}
	var contents []byte
	found := false
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, multipartReadError(nextErr)
		}
		if part.FormName() != field {
			if _, err := io.Copy(io.Discard, part); err != nil {
				_ = part.Close()
				return nil, multipartReadError(err)
			}
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			return nil, domain.NewValidationError("multipart."+field, "must appear exactly once")
		}
		found = true
		buffer := bytes.NewBuffer(nil)
		if _, err := io.Copy(buffer, part); err != nil {
			_ = part.Close()
			return nil, multipartReadError(err)
		}
		_ = part.Close()
		contents = buffer.Bytes()
	}
	if !found {
		return nil, domain.NewValidationError("multipart."+field, "is required")
	}
	return contents, nil
}

func multipartReadError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return domain.NewValidationError("body", "exceeds the request size limit")
	}
	return domain.NewValidationError("multipart", fmt.Sprintf("could not be read: %v", err))
}

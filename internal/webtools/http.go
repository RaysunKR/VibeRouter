package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxResponseBytes = 16 << 20

func (s *Service) postJSON(ctx context.Context, provider providerRuntime, path string, payload, output any) error {
	endpoint, err := providerEndpoint(provider, path)
	if err != nil {
		return err
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", provider.id, err)
	}

	requestContext, cancel := context.WithTimeout(ctx, provider.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("create %s request: %w", provider.id, err)
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%s request failed: %s", provider.id, redactSecret(err.Error(), provider.apiKey))
	}
	defer response.Body.Close()

	body, err := readResponseBody(response.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", provider.id, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := responseErrorMessage(body)
		if message != "" {
			return fmt.Errorf("%s request returned HTTP %d: %s", provider.id, response.StatusCode, redactSecret(message, provider.apiKey))
		}
		return fmt.Errorf("%s request returned HTTP %d", provider.id, response.StatusCode)
	}
	if err := checkResponseError(body); err != nil {
		return fmt.Errorf("%s response error: %s", provider.id, redactSecret(err.Error(), provider.apiKey))
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode %s response: %w", provider.id, err)
	}
	return nil
}

func providerEndpoint(provider providerRuntime, path string) (string, error) {
	base, err := url.Parse(provider.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return "", fmt.Errorf("invalid base URL for web tool provider %q", provider.id)
	}
	return strings.TrimRight(provider.baseURL, "/") + path, nil
}

func readResponseBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("response exceeds 16 MiB limit")
	}
	return data, nil
}

type responseEnvelope struct {
	Error   json.RawMessage `json:"error"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Detail  json.RawMessage `json:"detail"`
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
}

func checkResponseError(body []byte) error {
	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	if rawMessageHasValue(envelope.Error) {
		return errors.New(errorMessage(envelope, envelope.Error))
	}
	if envelope.Success != nil && !*envelope.Success {
		return errors.New(errorMessage(envelope, nil))
	}
	if rawMessageHasValue(envelope.Code) && !successfulCode(envelope.Code) {
		return errors.New(errorMessage(envelope, nil))
	}
	return nil
}

func responseErrorMessage(body []byte) string {
	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if rawMessageHasValue(envelope.Error) || envelope.Message != "" || envelope.Msg != "" || rawMessageHasValue(envelope.Detail) || rawMessageHasValue(envelope.Code) {
		return errorMessage(envelope, nil)
	}
	return ""
}

func errorMessage(envelope responseEnvelope, preferred json.RawMessage) string {
	for _, raw := range []json.RawMessage{preferred, envelope.Error, json.RawMessage(strconv.Quote(envelope.Message)), json.RawMessage(strconv.Quote(envelope.Msg)), envelope.Detail, envelope.Code} {
		if message := rawMessageText(raw); message != "" {
			return message
		}
	}
	return "unknown upstream error"
}

func rawMessageText(raw json.RawMessage) string {
	if !rawMessageHasValue(raw) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object struct {
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Detail  json.RawMessage `json:"detail"`
		Code    json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, candidate := range []string{object.Message, object.Msg, rawMessageText(object.Detail), rawMessageText(object.Code)} {
			if strings.TrimSpace(candidate) != "" {
				return strings.TrimSpace(candidate)
			}
		}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "map[]" {
			return text
		}
	}
	return ""
}

func rawMessageHasValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != `""` && trimmed != "{}"
}

func successfulCode(raw json.RawMessage) bool {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number == 0 || number == 200
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "", "0", "200", "ok", "success":
			return true
		}
	}
	return false
}

func redactSecret(message, secret string) string {
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "[redacted]")
}

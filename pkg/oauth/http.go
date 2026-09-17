package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maxDrainBytes = 32 << 10

func (p *providerCore) getJSON(
	ctx context.Context,
	endpoint string,
	accessToken string,
	target any,
	operation string,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return newProviderError(ErrInvalidConfiguration, p.name, operation)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("User-Agent", "go-common-oauth/1")

	response, err := p.httpClient.Do(request)
	if err != nil {
		return p.transportError(ctx, operation, err)
	}
	defer closeAndDrain(response.Body)

	if response.StatusCode != http.StatusOK {
		return p.statusError(operation, response.StatusCode)
	}
	return p.decodeJSONResponse(ctx, response, target, operation)
}

func (p *providerCore) decodeJSONResponse(
	ctx context.Context,
	response *http.Response,
	target any,
	operation string,
) error {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return newProviderError(ErrMalformedResponse, p.name, operation)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, p.maxResponseBytes+1))
	if err != nil {
		return p.transportError(ctx, operation, err)
	}
	if int64(len(data)) > p.maxResponseBytes {
		return newProviderError(ErrResponseTooLarge, p.name, operation)
	}
	if err := validateUniqueObjectKeys(data); err != nil {
		return newProviderError(ErrMalformedResponse, p.name, operation)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return newProviderError(ErrMalformedResponse, p.name, operation)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return newProviderError(ErrMalformedResponse, p.name, operation)
	}
	return nil
}

// transportError keeps cancellation and timeout semantics stable even when
// http.Client's deadline wins the race with the exchange context's identical
// deadline. Provider/network failures remain deliberately redacted.
func (p *providerCore) transportError(
	ctx context.Context,
	operation string,
	cause error,
) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	switch {
	case errors.Is(cause, context.Canceled):
		return context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return newProviderError(ErrProviderUnavailable, p.name, operation)
	}
}

func closeAndDrain(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, body, maxDrainBytes)
	_ = body.Close()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return ErrMalformedResponse
}

func validateUniqueObjectKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrMalformedResponse
			}
			if _, duplicate := keys[key]; duplicate {
				return ErrMalformedResponse
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrMalformedResponse
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrMalformedResponse
		}
	default:
		return ErrMalformedResponse
	}
	return nil
}

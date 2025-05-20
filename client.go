package gkBoot

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	http2 "golang.org/x/net/http2"

	"github.com/yomiji/gkBoot/helpers"
	"github.com/yomiji/gkBoot/request"
	"github.com/yomiji/gkBoot/response"
)

var (
	MalformedRequestErr = errors.New("malformed request")
	HTTP2GlobalCA       = []*tls.Config{nil}
)

// SkipClientValidation is an interface that can be implemented by a request object to skip client validation
// during the generation of an *http.Request object. If a request object implements this interface, the validation
// step will be bypassed and the request object will not be validated before generating the HTTP request.
//
// Example Usage:
//
//    type MyRequest struct {
//        // request fields
//    }
//
//    func (r *MyRequest) Info() request.HttpRouteInfo {
//        // return HttpRouteInfo
//    }
//
//    func (r *MyRequest) Validate() error {
//        // return validation error
//    }
//
//    func (r *MyRequest) SkipClientValidation() {
//        // implement the interface to skip client validation
//    }
//
//    func main() {
//        request := &MyRequest{}
//
//        // Generate *http.Request object
//        httpRequest, err := GenerateClientRequest(baseUrl, request)
//        if err != nil {
//            // Handle error
//        }
//        // Use the *http.Request object for making the HTTP request
//    }
type SkipClientValidation interface {
	SkipClientValidation()
}

// UsingSkipClientValidation is a type that can be used to indicate that client
// validation should be skipped during the generation of an *http.Request object.
// If a request object contains a field of type UsingSkipClientValidation and is
// used as an argument for generating an *http.Request object, the validation step
// will be bypassed and the request object will not be validated before generating
// the HTTP request.
type UsingSkipClientValidation struct{}

func (u UsingSkipClientValidation) SkipClientValidation() {}

// Requester is an interface that defines the Request method for making HTTP requests.
//
// The Request method takes a context.Context object as a parameter and returns an *http.Request object
// and an error. The ctx parameter is used to pass any optional cancellation signals or deadlines to the
// Request method.
//
// The Request method is responsible for creating and returning an *http.Request object that represents
// the HTTP request to be made. If there is an error while creating the request, the error should be
// returned.
//
// Example Usage:
//
//    type MyRequester struct {}
//
//    func (r *MyRequester) Request(ctx context.Context) (*http.Request, error) {
//        // Implement the logic to create and return the *http.Request object
//    }
//
//    func main() {
//        requester := &MyRequester{}
//        request, err := requester.Request(context.Background())
//        if err != nil {
//            // Handle error
//        }
//        // Use the *http.Request object for making the HTTP request
//    }
type Requester interface {
	Request(ctx context.Context) (*http.Request, error)
}

func GenerateClientRequest(baseUrl string, serviceRequest request.HttpRequest) (*http.Request, error) {
	if serviceRequest == nil {
		return nil, fmt.Errorf("nil client not supported")
	}

	if validator, ok := serviceRequest.(request.Validator); ok {
		if _, shouldSkip := serviceRequest.(SkipClientValidation); !shouldSkip {
			if validationErr := validator.Validate(); validationErr != nil {
				return nil, fmt.Errorf("client validation err: %w", validationErr)
			}
		}
	}

	// make base url
	var srPath = serviceRequest.Info().Path
	baseUrl = strings.TrimRight(baseUrl, "/")
	srPath = strings.TrimLeft(srPath, "/")
	var joinedStr = baseUrl + "/" + srPath
	u, err := url.Parse(joinedStr)
	if err != nil {
		return nil, fmt.Errorf("client generation failed, %s, attempted url: %s", err, joinedStr)
	}

	var srMethod = serviceRequest.Info().Method

	// shortcut request generation using a Requester
	if requester, ok := serviceRequest.(Requester); ok {
		var r *http.Request
		r, err = requester.Request(context.Background())
		if err != nil {
			return nil, fmt.Errorf("client generation failed [%s] %w %w", joinedStr, err, MalformedRequestErr)
		}
		r.URL = u
		r.Method = string(srMethod)
		return r, nil
	}

	clientValue := reflect.ValueOf(serviceRequest)

	// Deref one layer of pointer if necessary
	if clientValue.Kind() == reflect.Ptr {
		clientValue = clientValue.Elem()
	}

	// Check if we have a struct (required)
	if clientValue.Kind() != reflect.Struct {
		return nil, fmt.Errorf("non-struct client not supported")
	}

	var srName = serviceRequest.Info().Name

	var requestResult *http.Request

	if _, ok := serviceRequest.(jsonBody); ok {
		var body []byte

		body, err = json.Marshal(serviceRequest)
		if err != nil {
			return nil, fmt.Errorf("client generation failed, %s, of client %s", err, srName)
		}

		requestResult, err = http.NewRequest(string(srMethod), u.String(), bytes.NewReader(body))
	} else {
		requestResult, err = http.NewRequest(string(srMethod), u.String(), nil)
	}

	err = assignRequest(requestResult, clientValue)
	if err != nil {
		return requestResult, fmt.Errorf("client field assignment failed, for client %s: %w", srName, err)
	}

	return requestResult, nil
}

func DoRequest[RequestType request.HttpRequest, ResponseType any](
		baseUrl string,
		clientRequest RequestType,
		responseObj *ResponseType,
		tlsConfig ...*tls.Config,
) error {
	c, err := GenerateClientRequest(baseUrl, clientRequest)
	if err != nil {
		return err
	}

	return DoGeneratedRequest[ResponseType](c, responseObj, tlsConfig...)
}

func DoGeneratedRequest[ResponseType any](
		r *http.Request, responseObj *ResponseType, tlsConfig ...*tls.Config,
) error {
	client := http.DefaultClient

	if len(tlsConfig) > 0 {
		client.Transport = &http2.Transport{TLSClientConfig: tlsConfig[0]}
	}

	resp, err := client.Do(r)
	if err != nil {
		return err
	}

	var temp interface{} = responseObj

	if statusCoder, ok := temp.(response.CodedResponse); ok {
		statusCoder.NewCode(resp.StatusCode)
	}

	if captureReader, ok := temp.(response.CaptureReader); ok {
		err = captureReader.Capture(resp.Body)
		if err != nil {
			return fmt.Errorf("unable to capture response body for %s %s due to %s", r.Method, r.URL, err)
		}

		return nil
	}

	defer resp.Body.Close()

	var body []byte

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unable to parse response body for %s %s due to %s", r.Method, r.URL, err)
	}

	// if the response object is nil, only non-200 indicates error
	if responseObj == nil {
		if resp.StatusCode != 200 {
			errorObj := struct {
				response.ErrorResponse
			}{}
			errorObj.NewError(resp.StatusCode, http.StatusText(resp.StatusCode), "body", body)

			return errorObj
		}

		return nil
	}

	if erredResponse, ok := temp.(response.ErredResponse); ok {
		if resp.StatusCode != http.StatusOK {
			erredResponse.NewError(resp.StatusCode, "from response: %s", body)
		}
	}

	if unmarshalAble, ok := temp.(json.Unmarshaler); ok {
		err = unmarshalAble.UnmarshalJSON(body)
		if err != nil {
			return fmt.Errorf("unable to decode response body for %s %s due to %s", r.Method, r.URL, err)
		}

		return nil
	}

	return json.Unmarshal(body, responseObj)
}

func assignRequest(r *http.Request, value reflect.Value) error {
	baseVal := value
	baseValType := value.Type()
	baseValKind := baseValType.Kind()

	// ensure that the first value is always a kind of struct
	if baseValKind != reflect.Struct {
		if baseVal.CanInterface() {
			objName := helpers.GetFriendlyRequestName(baseVal.Interface())
			return fmt.Errorf("request object '%s' must be a Struct type", objName)
		} else {
			return fmt.Errorf("request object must be a Struct type for path %s", r.URL.RawPath)
		}
	}

	// iterate over all the fields in the struct
	for i := 0; i < baseValType.NumField(); i++ {
		var err error

		fieldDesc := baseValType.Field(i)

		fieldVal := baseVal.Field(i)

		// if it is a pointer we need to init and get the element that is the concrete val
		if fieldDesc.Type.Kind() == reflect.Ptr {
			// traverse pointer set
			for ; !fieldVal.IsZero() && fieldVal.Type().Kind() == reflect.Ptr; fieldVal = fieldVal.Elem() {
			}
		}

		requestTag, alias, jsonAlias, encode := readClientTag(fieldDesc)

		urlEncode, _ := strconv.ParseBool(encode)

		if requestTag == "" && (fieldDesc.Type.Kind() == reflect.Struct || (fieldDesc.Anonymous && fieldVal.CanSet())) {
			// recurse if embedded structure
			return assignRequest(r, fieldVal)
		} else if requestTag == "form" {
			fieldName := fieldDesc.Name

			if jsonAlias != "" {
				fieldName = jsonAlias
			}

			if alias != "" {
				fieldName = alias
			}

			err = writeRequestBody(r, fieldName, fieldVal)
			if err != nil {
				return err
			}
		} else if requestTag != "" {
			operation := returnClientOperationByTagValue(requestTag)
			if operation == nil {
				return fmt.Errorf("unknown 'client' operation: %s", requestTag)
			}

			fieldName := fieldDesc.Name

			if jsonAlias != "" {
				fieldName = jsonAlias
			}

			if alias != "" {
				fieldName = alias
			}

			err = operation(r, fieldName, fieldVal, strings.HasSuffix(requestTag, "!"), urlEncode)
			if err != nil {
				return err
			}
		} else {
			continue
		}
	}

	return nil
}

func readClientTag(field reflect.StructField) (requestPart, alias, jsonAlias, encode string) {
	var ok bool
	var tag string

	if tag, ok = field.Tag.Lookup("urlEncode"); ok {
		encode = tag
	}
	if requestPart, alias, jsonAlias, ok = fromSwaggestTag(field); ok {
		return requestPart, alias, jsonAlias, encode
	}
	if tag, ok = field.Tag.Lookup("request"); ok {
		requestPart = tag
	}
	if tag, ok = field.Tag.Lookup("alias"); ok {
		alias = tag
	}
	if tag, ok = field.Tag.Lookup("json"); ok {
		if tag == "-," {
			jsonAlias = "-"
		} else {
			jsonAlias = strings.Split(tag, ",")[0]
			if jsonAlias == "-" {
				jsonAlias = ""
			}
		}
	}

	return
}

func convertBaseValueToString(src reflect.Value, urlEncode bool) *string {
	if !src.IsValid() {
		return nil
	}

	srcType := src.Type()

	if srcType.Kind() == reflect.Ptr {
		src = src.Elem()
		return convertBaseValueToString(src, urlEncode)
	}

	kind := src.Type().Kind()

	var result string

	switch kind {
	case reflect.String:
		result = src.String()
	case reflect.Int:
		result = strconv.FormatInt(src.Int(), 10)
	case reflect.Bool:
		result = strconv.FormatBool(src.Bool())
	case reflect.Slice:
		result = convertSliceToStringValue(src, urlEncode)
		return &result
	case reflect.Float64:
		result = strconv.FormatFloat(src.Float(), 'f', -1, 64)
	case reflect.Uint:
		result = strconv.FormatUint(src.Uint(), 10)
	case reflect.Uint64:
		result = strconv.FormatUint(src.Uint(), 10)
	case reflect.Float32:
		result = strconv.FormatFloat(src.Float(), 'f', -1, 32)
	case reflect.Int8:
		result = strconv.FormatInt(src.Int(), 10)
	case reflect.Uint8:
		result = strconv.FormatUint(src.Uint(), 10)
	case reflect.Int64:
		result = strconv.FormatInt(src.Int(), 10)
	case reflect.Int32:
		result = strconv.FormatInt(src.Int(), 10)
	case reflect.Int16:
		result = strconv.FormatInt(src.Int(), 10)
	case reflect.Uint16:
		result = strconv.FormatUint(src.Uint(), 10)
	case reflect.Uint32:
		result = strconv.FormatUint(src.Uint(), 10)
	case reflect.Complex64:
		result = strconv.FormatComplex(src.Complex(), 'f', -1, 64)
	case reflect.Complex128:
		result = strconv.FormatComplex(src.Complex(), 'f', -1, 128)
	case reflect.Struct:
		if src.CanInterface() {
			body, err := json.Marshal(src.Interface())
			if err != nil {
				result = "{ \"error\": \"JSON parse error\" }"
			}

			result = string(body)
		} else {
			result = "null"
		}
	default:
		result = "?"
	}

	if urlEncode {
		result = url.QueryEscape(result)
	}

	return &result
}

func convertSliceToStringValue(value reflect.Value, urlEncode bool) string {
	var accumulatedStrArr = make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		var currentStr *string

		currentStr = convertBaseValueToString(value.Index(i), urlEncode)
		if currentStr == nil {
			continue
		}

		if urlEncode {
			*currentStr = url.QueryEscape(*currentStr)
		}

		accumulatedStrArr = append(accumulatedStrArr, *currentStr)
	}

	return strings.Join(accumulatedStrArr, ",")
}

type typicalClientRequestWriter func(
		r *http.Request, fieldName string, fieldValue reflect.Value, isRequired bool,
		urlEncode bool,
) error

func returnClientOperationByTagValue(tagName string) typicalClientRequestWriter {
	switch tagName {
	case "cookie", "cookie!":
		return writeRequestCookie
	case "header", "header!":
		return writeRequestHeader
	case "query", "query!":
		return writeRequestQueryParam
	case "path", "path!":
		return writeRequestPath
	default:
		return nil
	}
}

func writeRequestCookie(
		r *http.Request, fieldName string, fieldValue reflect.Value, isRequired bool,
		urlEncode bool,
) error {
	var convertedValue = convertBaseValueToString(fieldValue, urlEncode)

	if isRequired {
		if convertedValue == nil || *convertedValue == "" {
			return fmt.Errorf("required cookie not found or not set: %s", fieldName)
		}
	}

	var cookie = new(http.Cookie)
	cookie.Name = fieldName

	if convertedValue != nil {
		cookie.Value = *convertedValue
	} else {
		cookie.Value = ""
	}

	r.AddCookie(cookie)

	return nil
}

func writeRequestHeader(
		r *http.Request, fieldName string, fieldValue reflect.Value, isRequired bool,
		urlEncode bool,
) error {
	var convertedValue = convertBaseValueToString(fieldValue, urlEncode)

	if isRequired {
		if convertedValue == nil || *convertedValue == "" {
			return fmt.Errorf("required header not found or not set: %s", fieldName)
		}
	}

	if convertedValue != nil {
		r.Header.Add(fieldName, *convertedValue)
	} else {
		r.Header.Add(fieldName, "")
	}

	return nil
}

func writeRequestQueryParam(
		r *http.Request, fieldName string, fieldValue reflect.Value, isRequired bool, urlEncode bool,
) error {
	var convertedValue = convertBaseValueToString(fieldValue, false)

	if isRequired {
		if convertedValue == nil || *convertedValue == "" {
			return fmt.Errorf("required header not found or not set: %s", fieldName)
		}
	}

	if convertedValue != nil {
		reqQuery := r.URL.Query()
		reqQuery.Add(fieldName, *convertedValue)
		r.URL.RawQuery = reqQuery.Encode()
	} else {
		reqQuery := r.URL.Query()
		reqQuery.Add(fieldName, "")
		r.URL.RawQuery = reqQuery.Encode()
	}

	return nil
}

func writeRequestBody(r *http.Request, fieldName string, fieldValue reflect.Value) error {
	r.Header.Set("Content-Type", "application/json")

	if fieldValue.CanInterface() {
		jsBody, err := json.Marshal(fieldValue.Interface())
		if err != nil {
			return fmt.Errorf("client generation failed, %s, of client field %s", err, fieldName)
		}

		r.Body = io.NopCloser(bytes.NewReader(jsBody))
	} else {
		return fmt.Errorf("client generation failed, unable to get body of client field %s", fieldName)
	}

	return nil
}

func writeRequestPath(
		r *http.Request, fieldName string, fieldValue reflect.Value, isRequired bool,
		urlEncode bool,
) error {
	var convertedValue = convertBaseValueToString(fieldValue, urlEncode)

	if isRequired {
		if convertedValue == nil || *convertedValue == "" {
			return fmt.Errorf("required path variable not found or not set: %s", fieldName)
		}
	}

	path := r.URL.Path

	replaceableString := "{" + fieldName + "}"

	if !strings.Contains(path, replaceableString) {
		return fmt.Errorf(
			"could not find path variable: %s, in path [%s], wanted syntax [%s]", fieldName, path,
			replaceableString,
		)
	}

	if convertedValue != nil {
		path = strings.Replace(path, replaceableString, *convertedValue, -1)
	} else {
		path = strings.Replace(r.URL.Path, replaceableString, "", -1)
	}

	r.URL.Path = path

	return nil
}

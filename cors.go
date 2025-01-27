package handlers

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSOption represents a functional option for configuring the CORS middleware.
type CORSOption func(*cors) error

type cors struct {
	h                      http.Handler
	allowedHeaders         []string
	allowedHeadersFunc     func(r *http.Request) []string
	allowedMethods         []string
	allowedOrigins         []string
	allowedOriginsFunc     func(r *http.Request) []string
	allowedOriginValidator OriginValidator
	exposedHeaders         []string
	maxAge                 int
	ignoreOptions          bool
	allowCredentials       bool
	allowDefaultOrigins    bool
	defaultOrigin          string
	optionStatusCode       int
}

// OriginValidator takes an origin string and returns whether or not that origin is allowed.
type OriginValidator func(string) bool

var (
	defaultCorsOptionStatusCode = 200
	defaultCorsMethods          = []string{"GET", "HEAD", "POST"}
	defaultCorsHeaders          = []string{"Accept", "Accept-Language", "Content-Language", "Origin"}
	// (WebKit/Safari v9 sends the Origin header by default in AJAX requests)
)

const (
	corsOptionMethod           string = "OPTIONS"
	corsAllowOriginHeader      string = "Access-Control-Allow-Origin"
	corsExposeHeadersHeader    string = "Access-Control-Expose-Headers"
	corsMaxAgeHeader           string = "Access-Control-Max-Age"
	corsAllowMethodsHeader     string = "Access-Control-Allow-Methods"
	corsAllowHeadersHeader     string = "Access-Control-Allow-Headers"
	corsAllowCredentialsHeader string = "Access-Control-Allow-Credentials"
	corsRequestMethodHeader    string = "Access-Control-Request-Method"
	corsRequestHeadersHeader   string = "Access-Control-Request-Headers"
	corsOriginHeader           string = "Origin"
	corsVaryHeader             string = "Vary"
	corsOriginMatchAll         string = "*"
)

func (ch *cors) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get(corsOriginHeader)
	if !ch.isOriginAllowed(r, origin) {
		if r.Method != corsOptionMethod || ch.ignoreOptions {
			ch.h.ServeHTTP(w, r)
		}

		return
	}

	if r.Method == corsOptionMethod {
		if ch.ignoreOptions {
			ch.h.ServeHTTP(w, r)
			return
		}

		if _, ok := r.Header[corsRequestMethodHeader]; !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		method := r.Header.Get(corsRequestMethodHeader)
		if !isMatch(method, ch.allowedMethods) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		referenceAllowedHeaders := ch.allowedHeaders

		if ch.allowedHeadersFunc != nil {
			referenceAllowedHeaders = combineAllowedHeaders(referenceAllowedHeaders, ch.allowedHeadersFunc(r))
		}

		requestHeaders := strings.Split(r.Header.Get(corsRequestHeadersHeader), ",")
		allowedHeaders := []string{}
		for _, v := range requestHeaders {
			canonicalHeader := http.CanonicalHeaderKey(strings.TrimSpace(v))
			if canonicalHeader == "" || isMatch(canonicalHeader, defaultCorsHeaders) {
				continue
			}

			// TODO - make local
			if !isMatch(canonicalHeader, referenceAllowedHeaders) {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			allowedHeaders = append(allowedHeaders, canonicalHeader)
		}

		if len(allowedHeaders) > 0 {
			w.Header().Set(corsAllowHeadersHeader, strings.Join(allowedHeaders, ","))
		}

		if ch.maxAge > 0 {
			w.Header().Set(corsMaxAgeHeader, strconv.Itoa(ch.maxAge))
		}

		if !isMatch(method, defaultCorsMethods) {
			w.Header().Set(corsAllowMethodsHeader, method)
		}
	} else {
		if len(ch.exposedHeaders) > 0 {
			w.Header().Set(corsExposeHeadersHeader, strings.Join(ch.exposedHeaders, ","))
		}
	}

	if ch.allowCredentials {
		w.Header().Set(corsAllowCredentialsHeader, "true")
	}

	referenceAllowedOrigins := ch.getAllowedOrigins(r)

	if len(referenceAllowedOrigins) > 1 {
		w.Header().Set(corsVaryHeader, corsOriginHeader)
	}

	returnOrigin := origin
	if ch.allowedOriginValidator == nil && len(referenceAllowedOrigins) == 0 {
		returnOrigin = ch.defaultOrigin
	} else {
		for _, o := range referenceAllowedOrigins {
			// A configuration of * is different than explicitly setting an allowed
			// origin. Returning arbitrary origin headers in an access control allow
			// origin header is unsafe and is not required by any use case.
			if o == corsOriginMatchAll {
				returnOrigin = "*"
				break
			}
		}
	}
	w.Header().Set(corsAllowOriginHeader, returnOrigin)

	if r.Method == corsOptionMethod {
		w.WriteHeader(ch.optionStatusCode)
		return
	}
	ch.h.ServeHTTP(w, r)
}

// CORS provides Cross-Origin Resource Sharing middleware.
// Example:
//
//  import (
//      "net/http"
//
//      "github.com/gorilla/handlers"
//      "github.com/gorilla/mux"
//  )
//
//  func main() {
//      r := mux.NewRouter()
//      r.HandleFunc("/users", UserEndpoint)
//      r.HandleFunc("/projects", ProjectEndpoint)
//
//      // Apply the CORS middleware to our top-level router, with the defaults.
//      http.ListenAndServe(":8000", handlers.CORS()(r))
//  }
//
func CORS(opts ...CORSOption) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		ch := parseCORSOptions(opts...)
		ch.h = h
		return ch
	}
}

func parseCORSOptions(opts ...CORSOption) *cors {
	ch := &cors{
		allowedMethods:      defaultCorsMethods,
		allowedHeaders:      defaultCorsHeaders,
		allowedOrigins:      []string{},
		optionStatusCode:    defaultCorsOptionStatusCode,
		allowDefaultOrigins: true,
		defaultOrigin:       "*",
	}

	for _, option := range opts {
		option(ch)
	}

	return ch
}

//
// Functional options for configuring CORS.
//

// AllowedHeaders adds the provided headers to the list of allowed headers in a
// CORS request.
// This is an append operation so the headers Accept, Accept-Language,
// and Content-Language are always allowed.
// Content-Type must be explicitly declared if accepting Content-Types other than
// application/x-www-form-urlencoded, multipart/form-data, or text/plain.
func AllowedHeaders(headers []string) CORSOption {
	return func(ch *cors) error {

		ch.allowedHeaders = combineAllowedHeaders(ch.allowedHeaders, headers)
		return nil
	}
}

// Disallows default origins
func DisallowDefaultOrigins() CORSOption {
	return func(ch *cors) error {

		ch.allowDefaultOrigins = false
		return nil
	}
}

func combineAllowedHeaders(existing, add []string) []string {

	result := existing

	for _, v := range add {
		normalizedHeader := http.CanonicalHeaderKey(strings.TrimSpace(v))
		if normalizedHeader == "" {
			continue
		}

		if !isMatch(normalizedHeader, existing) {
			result = append(result, normalizedHeader)
		}
	}

	return result
}

// AllowedHeaders creates a function which appends the allowed headers per
// CORS request.
// This is an append operation so the headers Accept, Accept-Language,
// and Content-Language are always allowed.
// Content-Type must be explicitly declared if accepting Content-Types other than
// application/x-www-form-urlencoded, multipart/form-data, or text/plain.
func AllowedHeadersFunc(input func(r *http.Request) []string) CORSOption {
	return func(ch *cors) error {
		ch.allowedHeadersFunc = input
		return nil
	}
}

// AllowedMethods can be used to explicitly allow methods in the
// Access-Control-Allow-Methods header.
// This is a replacement operation so you must also
// pass GET, HEAD, and POST if you wish to support those methods.
func AllowedMethods(methods []string) CORSOption {
	return func(ch *cors) error {
		ch.allowedMethods = []string{}
		for _, v := range methods {
			normalizedMethod := strings.ToUpper(strings.TrimSpace(v))
			if normalizedMethod == "" {
				continue
			}

			if !isMatch(normalizedMethod, ch.allowedMethods) {
				ch.allowedMethods = append(ch.allowedMethods, normalizedMethod)
			}
		}

		return nil
	}
}

// AllowedOrigins sets the allowed origins for CORS requests, as used in the
// 'Allow-Access-Control-Origin' HTTP header.
// Note: Passing in a []string{"*"} will allow any domain.
func AllowedOrigins(origins []string) CORSOption {
	return func(ch *cors) error {
		ch.allowedOrigins = filterAllowedOrigins(origins)
		return nil
	}
}

// AllowedOrigins sets the allowed origins for CORS requests based on the
// result of a function, as used in the
// 'Allow-Access-Control-Origin' HTTP header.
// Note: Passing in a []string{"*"} will allow any domain.
func AllowedOriginsFunc(input func(req *http.Request) []string) CORSOption {
	return func(ch *cors) error {
		ch.allowedOriginsFunc = func(req *http.Request) []string {
			return filterAllowedOrigins(input(req))
		}
		return nil
	}
}

func filterAllowedOrigins(input []string) []string {

	for _, v := range input {
		if v == corsOriginMatchAll {
			return []string{corsOriginMatchAll}
		}
	}
	return input
}

// AllowedOriginValidator sets a function for evaluating allowed origins in CORS requests, represented by the
// 'Allow-Access-Control-Origin' HTTP header.
func AllowedOriginValidator(fn OriginValidator) CORSOption {
	return func(ch *cors) error {
		ch.allowedOriginValidator = fn
		return nil
	}
}

// OptionStatusCode sets a custom status code on the OPTIONS requests.
// Default behaviour sets it to 200 to reflect best practices. This is option is not mandatory
// and can be used if you need a custom status code (i.e 204).
//
// More informations on the spec:
// https://fetch.spec.whatwg.org/#cors-preflight-fetch
func OptionStatusCode(code int) CORSOption {
	return func(ch *cors) error {
		ch.optionStatusCode = code
		return nil
	}
}

// ExposedHeaders can be used to specify headers that are available
// and will not be stripped out by the user-agent.
func ExposedHeaders(headers []string) CORSOption {
	return func(ch *cors) error {
		ch.exposedHeaders = []string{}
		for _, v := range headers {
			normalizedHeader := http.CanonicalHeaderKey(strings.TrimSpace(v))
			if normalizedHeader == "" {
				continue
			}

			if !isMatch(normalizedHeader, ch.exposedHeaders) {
				ch.exposedHeaders = append(ch.exposedHeaders, normalizedHeader)
			}
		}

		return nil
	}
}

// MaxAge determines the maximum age (in seconds) between preflight requests. A
// maximum of 10 minutes is allowed. An age above this value will default to 10
// minutes.
func MaxAge(age int) CORSOption {
	return func(ch *cors) error {
		// Maximum of 10 minutes.
		if age > 600 {
			age = 600
		}

		ch.maxAge = age
		return nil
	}
}

// IgnoreOptions causes the CORS middleware to ignore OPTIONS requests, instead
// passing them through to the next handler. This is useful when your application
// or framework has a pre-existing mechanism for responding to OPTIONS requests.
func IgnoreOptions() CORSOption {
	return func(ch *cors) error {
		ch.ignoreOptions = true
		return nil
	}
}

// AllowCredentials can be used to specify that the user agent may pass
// authentication details along with the request.
func AllowCredentials() CORSOption {
	return func(ch *cors) error {
		ch.allowCredentials = true
		return nil
	}
}

func (ch *cors) isOriginAllowed(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}

	allowedOrigins := ch.getAllowedOrigins(r)

	if ch.allowedOriginValidator != nil {
		return ch.allowedOriginValidator(origin)
	}

	if len(allowedOrigins) == 0 {
		return ch.allowDefaultOrigins
	}

	for _, allowedOrigin := range allowedOrigins {
		if allowedOrigin == origin || allowedOrigin == corsOriginMatchAll {
			return true
		}
	}

	return false
}

func (ch *cors) getAllowedOrigins(r *http.Request) []string {
	if ch.allowedOriginsFunc != nil {
		return ch.allowedOriginsFunc(r)
	} else {
		// this gets filtered on construction by the options
		return ch.allowedOrigins
	}

}

func isMatch(needle string, haystack []string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}

	return false
}

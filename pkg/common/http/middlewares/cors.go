package middlewares

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxCORSMaxAge = 24 * time.Hour

type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

type corsConfig struct {
	origins           map[string]struct{}
	wildcard          bool
	methods           map[string]struct{}
	methodList        string
	headers           map[string]struct{}
	headerList        string
	exposedHeaderList string
	allowCredentials  bool
	maxAgeSeconds     string
}

// CORS constructs exact-origin CORS middleware.
func CORS(options CORSOptions) (gin.HandlerFunc, error) {
	config, err := normalizeCORS(options)
	if err != nil {
		return nil, err
	}
	return config.middleware(), nil
}

func normalizeCORS(options CORSOptions) (corsConfig, error) {
	if len(options.AllowedOrigins) == 0 || len(options.AllowedMethods) == 0 {
		return corsConfig{}, errors.New("cors: origins and methods are required")
	}
	if options.MaxAge < 0 || options.MaxAge > maxCORSMaxAge {
		return corsConfig{}, errors.New("cors: invalid max age")
	}

	config := corsConfig{
		origins:          make(map[string]struct{}, len(options.AllowedOrigins)),
		methods:          make(map[string]struct{}, len(options.AllowedMethods)),
		headers:          make(map[string]struct{}, len(options.AllowedHeaders)),
		allowCredentials: options.AllowCredentials,
	}
	for _, raw := range options.AllowedOrigins {
		if raw == "*" {
			config.wildcard = true
			continue
		}
		origin, err := normalizeOrigin(raw)
		if err != nil {
			return corsConfig{}, err
		}
		config.origins[origin] = struct{}{}
	}
	if config.wildcard && options.AllowCredentials {
		return corsConfig{}, errors.New("cors: wildcard origin cannot allow credentials")
	}
	if config.wildcard && len(config.origins) > 0 {
		return corsConfig{}, errors.New("cors: wildcard cannot be combined with exact origins")
	}

	methods := make([]string, 0, len(options.AllowedMethods))
	for _, method := range options.AllowedMethods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !validHTTPToken(method) {
			return corsConfig{}, errors.New("cors: invalid method")
		}
		if _, duplicate := config.methods[method]; !duplicate {
			config.methods[method] = struct{}{}
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)
	config.methodList = strings.Join(methods, ", ")

	headers, headerSet, err := normalizeHeaderNames(options.AllowedHeaders)
	if err != nil {
		return corsConfig{}, err
	}
	config.headers = headerSet
	config.headerList = strings.Join(headers, ", ")
	exposed, _, err := normalizeHeaderNames(options.ExposedHeaders)
	if err != nil {
		return corsConfig{}, err
	}
	config.exposedHeaderList = strings.Join(exposed, ", ")
	if options.MaxAge > 0 {
		config.maxAgeSeconds = strconv.FormatInt(int64(options.MaxAge/time.Second), 10)
	}
	return config, nil
}

func (config corsConfig) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		originHeader := c.GetHeader("Origin")
		if originHeader == "" {
			c.Next()
			return
		}
		appendVary(c.Writer.Header(), "Origin")

		origin, err := normalizeOrigin(originHeader)
		allowed := err == nil && config.originAllowed(origin)
		preflight := c.Request.Method == http.MethodOptions &&
			c.GetHeader("Access-Control-Request-Method") != ""
		if !preflight {
			_, methodAllowed := config.methods[c.Request.Method]
			allowed = allowed && methodAllowed
		}
		if !allowed {
			if preflight {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		allowOrigin := originHeader
		if config.wildcard {
			allowOrigin = "*"
		}
		c.Header("Access-Control-Allow-Origin", allowOrigin)
		if config.allowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if config.exposedHeaderList != "" {
			c.Header("Access-Control-Expose-Headers", config.exposedHeaderList)
		}
		if !preflight {
			c.Next()
			return
		}

		requestedMethod := strings.ToUpper(strings.TrimSpace(c.GetHeader("Access-Control-Request-Method")))
		if _, exists := config.methods[requestedMethod]; !exists ||
			!config.requestHeadersAllowed(c.GetHeader("Access-Control-Request-Headers")) {
			c.Header("Access-Control-Allow-Origin", "")
			c.Header("Access-Control-Allow-Credentials", "")
			c.Header("Access-Control-Expose-Headers", "")
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		appendVary(c.Writer.Header(), "Access-Control-Request-Method")
		appendVary(c.Writer.Header(), "Access-Control-Request-Headers")
		c.Header("Access-Control-Allow-Methods", config.methodList)
		if config.headerList != "" {
			c.Header("Access-Control-Allow-Headers", config.headerList)
		}
		if config.maxAgeSeconds != "" {
			c.Header("Access-Control-Max-Age", config.maxAgeSeconds)
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}

func (config corsConfig) originAllowed(origin string) bool {
	if config.wildcard {
		return true
	}
	_, allowed := config.origins[origin]
	return allowed
}

func (config corsConfig) requestHeadersAllowed(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		normalized := strings.ToLower(strings.TrimSpace(header))
		if !validHTTPToken(normalized) {
			return false
		}
		if _, allowed := config.headers[normalized]; !allowed {
			return false
		}
	}
	return true
}

func normalizeOrigin(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 2048 {
		return "", errors.New("cors: invalid origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return "", errors.New("cors: invalid origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Path != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("cors: invalid origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", errors.New("cors: invalid origin")
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", errors.New("cors: invalid origin port")
		}
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, nil
}

func normalizeHeaderNames(input []string) ([]string, map[string]struct{}, error) {
	result := make([]string, 0, len(input))
	set := make(map[string]struct{}, len(input))
	for _, header := range input {
		header = http.CanonicalHeaderKey(strings.TrimSpace(header))
		if !validHTTPToken(header) {
			return nil, nil, errors.New("cors: invalid header")
		}
		normalized := strings.ToLower(header)
		if _, duplicate := set[normalized]; duplicate {
			continue
		}
		set[normalized] = struct{}{}
		result = append(result, header)
	}
	sort.Strings(result)
	return result, set, nil
}

func appendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// CORSMiddleware no longer reflects arbitrary origins. It is a same-origin
// compatibility facade and emits no cross-origin permission.
//
// Deprecated: construct CORS with an explicit allowlist.
func CORSMiddleware(ctx *gin.Context) {
	ctx.Next()
}

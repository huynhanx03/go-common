package middlewares

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'"
	defaultFrameOptions          = "DENY"
	defaultReferrerPolicy        = "no-referrer"
	defaultPermissionsPolicy     = "camera=(), geolocation=(), microphone=()"
	maxSecurityHeaderBytes       = 4096
	maxHSTSMaxAge                = 2 * 365 * 24 * time.Hour
)

type SecurityHeadersOptions struct {
	ContentSecurityPolicy string
	FrameOptions          string
	ReferrerPolicy        string
	PermissionsPolicy     string
	HSTSMaxAge            time.Duration
	HSTSIncludeSubDomains bool
	HSTSPreload           bool
}

// SecurityHeaders constructs secure response-header middleware.
func SecurityHeaders(options SecurityHeadersOptions) (gin.HandlerFunc, error) {
	if options.ContentSecurityPolicy == "" {
		options.ContentSecurityPolicy = defaultContentSecurityPolicy
	}
	if options.FrameOptions == "" {
		options.FrameOptions = defaultFrameOptions
	}
	if options.ReferrerPolicy == "" {
		options.ReferrerPolicy = defaultReferrerPolicy
	}
	if options.PermissionsPolicy == "" {
		options.PermissionsPolicy = defaultPermissionsPolicy
	}
	for _, value := range []string{
		options.ContentSecurityPolicy,
		options.FrameOptions,
		options.ReferrerPolicy,
		options.PermissionsPolicy,
	} {
		if !validHeaderValue(value) {
			return nil, errors.New("security headers: invalid value")
		}
	}
	if options.HSTSMaxAge < 0 || options.HSTSMaxAge > maxHSTSMaxAge {
		return nil, errors.New("security headers: invalid HSTS max age")
	}
	if options.HSTSPreload && (options.HSTSMaxAge < 365*24*time.Hour || !options.HSTSIncludeSubDomains) {
		return nil, errors.New("security headers: preload requires one year and subdomains")
	}

	hsts := ""
	if options.HSTSMaxAge > 0 {
		hsts = "max-age=" + strconv.FormatInt(int64(options.HSTSMaxAge/time.Second), 10)
		if options.HSTSIncludeSubDomains {
			hsts += "; includeSubDomains"
		}
		if options.HSTSPreload {
			hsts += "; preload"
		}
	}

	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", options.FrameOptions)
		c.Header("Referrer-Policy", options.ReferrerPolicy)
		c.Header("Content-Security-Policy", options.ContentSecurityPolicy)
		c.Header("Permissions-Policy", options.PermissionsPolicy)
		if hsts != "" && c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", hsts)
		}
		c.Next()
	}, nil
}

func validHeaderValue(value string) bool {
	return value != "" &&
		len(value) <= maxSecurityHeaderBytes &&
		!strings.ContainsAny(value, "\r\n\x00")
}

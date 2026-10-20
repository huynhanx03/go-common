package middlewares

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/security/authentication"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

// AuthorizationChecker adapts the transport-neutral Authorizer to Gin route
// resource/action checks.
type AuthorizationChecker struct {
	authorizer authorization.Authorizer
}

// PermissionRequirement identifies one exact resource/action alternative.
// RequireAnyPermission grants the request when at least one requirement is
// allowed; malformed or empty requirement sets fail closed.
type PermissionRequirement struct {
	Resource string
	Action   string
}

func NewAuthorizationChecker(authorizer authorization.Authorizer) *AuthorizationChecker {
	return &AuthorizationChecker{authorizer: authorizer}
}

func (checker *AuthorizationChecker) RequirePermission(resourceKey, requiredAction string) gin.HandlerFunc {
	return checker.RequireAnyPermission(PermissionRequirement{
		Resource: resourceKey,
		Action:   requiredAction,
	})
}

// RequireAnyPermission evaluates exact alternatives in order and stops on the
// first allow. Evaluation failures are never converted into a denial or hidden
// by a later alternative.
func (checker *AuthorizationChecker) RequireAnyPermission(
	requirements ...PermissionRequirement,
) gin.HandlerFunc {
	validRequirements := validPermissionRequirements(requirements)
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		principal, exists := authentication.FromContext(ctx)
		if !exists || !principal.Authenticated || principal.Validate(time.Now().UTC()) != nil {
			unauthorized(c)
			return
		}
		if checker == nil || checker.authorizer == nil || !validRequirements {
			internalServerError(c)
			return
		}

		for _, requirement := range requirements {
			decision, err := checker.authorizer.Authorize(ctx, authorization.Request{
				Principal: principal,
				Resource:  requirement.Resource,
				Action:    requirement.Action,
			})
			if err != nil {
				if ctx.Err() != nil {
					c.Abort()
					return
				}
				internalServerError(c)
				return
			}
			if decision.Allowed {
				c.Next()
				return
			}
		}
		response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(
			apperr.CodeForbidden,
			"forbidden",
			nil,
		))
		c.Abort()
	}
}

func validPermissionRequirements(requirements []PermissionRequirement) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		if requirement.Resource == "" || requirement.Action == "" ||
			strings.TrimSpace(requirement.Resource) != requirement.Resource ||
			strings.TrimSpace(requirement.Action) != requirement.Action {
			return false
		}
	}
	return true
}

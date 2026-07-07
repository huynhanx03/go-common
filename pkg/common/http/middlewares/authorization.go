package middlewares

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/constraints"
)

// AuthorizationEnforcer abstracts policy evaluation (e.g. a Casbin-backed
// service). Applications implement this against their own policy storage.
type AuthorizationEnforcer interface {
	Enforce(ctx context.Context, userID string, resource, action string) (bool, error)
}

// AuthorizationChecker checks route access through an AuthorizationEnforcer.
type AuthorizationChecker struct {
	enforcer AuthorizationEnforcer
}

func NewAuthorizationChecker(enforcer AuthorizationEnforcer) *AuthorizationChecker {
	return &AuthorizationChecker{enforcer: enforcer}
}

func (pc *AuthorizationChecker) RequirePermission(resourceKey string, requiredAction string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		userID, ok := ctx.Value(constraints.ContextKeyUserID).(string)
		if !ok {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "user not authenticated", nil))
			c.Abort()
			return
		}

		allowed, err := pc.enforcer.Enforce(ctx, userID, resourceKey, requiredAction)
		if err != nil {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "failed to check permissions", err))
			c.Abort()
			return
		}
		if !allowed {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "permission denied", nil))
			c.Abort()
			return
		}

		c.Next()
	}
}

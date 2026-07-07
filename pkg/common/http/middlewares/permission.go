package middlewares

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/constraints"
)

// cacheKeyPrefixRolePermissions prefixes local-cache keys for aggregated role permissions.
const cacheKeyPrefixRolePermissions = "role_perms::"

// PermissionProvider supplies identity data for permission checks. Applications
// implement this against their own user/role storage.
type PermissionProvider interface {
	// UserRoleID returns the role ID assigned to the given user.
	UserRoleID(ctx context.Context, userID string) (int, error)

	// RolePermissions returns the aggregated permissions of a role, including
	// permissions inherited from descendant roles, as map[resourceID]scopeMask.
	RolePermissions(ctx context.Context, roleID int) (map[int]int, error)

	// ResourceID resolves a resource key to its numeric ID from the
	// application's permission registry. Return 0 for unknown keys.
	ResourceID(resourceKey string) int
}

// PermissionChecker holds dependencies for permission checking with local cache.
type PermissionChecker struct {
	provider PermissionProvider
	cache    cache.LocalCache[string, any]
}

// NewPermissionChecker creates a new PermissionChecker instance.
func NewPermissionChecker(
	provider PermissionProvider,
	localCache cache.LocalCache[string, any],
) *PermissionChecker {
	return &PermissionChecker{
		provider: provider,
		cache:    localCache,
	}
}

// getRolePermissions fetches aggregated permissions for a role via the provider.
// Returns map[resourceID]scopeMask. Results are cached locally.
func (pc *PermissionChecker) getRolePermissions(ctx context.Context, roleID int) (map[int]int, error) {
	cacheKey := cacheKeyPrefixRolePermissions + strconv.Itoa(roleID)
	if perms, found := cache.Get[map[int]int](pc.cache, cacheKey); found {
		return perms, nil
	}

	perms, err := pc.provider.RolePermissions(ctx, roleID)
	if err != nil {
		return nil, err
	}

	cache.Set(pc.cache, cacheKey, perms)
	return perms, nil
}

// RequirePermission checks if the authenticated user's role has the required permission scope for a resource.
func (pc *PermissionChecker) RequirePermission(resourceKey string, requiredScope int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		userID, ok := ctx.Value(constraints.ContextKeyUserID).(string)
		if !ok {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "user not authenticated", nil))
			c.Abort()
			return
		}

		roleID, err := pc.provider.UserRoleID(ctx, userID)
		if err != nil {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "user not found", nil))
			c.Abort()
			return
		}

		perms, err := pc.getRolePermissions(ctx, roleID)
		if err != nil {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "failed to load permissions", nil))
			c.Abort()
			return
		}

		resourceID := pc.provider.ResourceID(resourceKey)
		if resourceID == 0 {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "unknown resource", nil))
			c.Abort()
			return
		}

		scopeMask, exists := perms[resourceID]
		if !exists || (scopeMask&requiredScope) != requiredScope {
			response.ErrorResponse(c, apperr.CodeForbidden, apperr.New(apperr.CodeForbidden, "permission denied", nil))
			c.Abort()
			return
		}

		c.Next()
	}
}

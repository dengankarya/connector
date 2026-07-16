package admin

import (
	"net/http"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// RequirePermission returns a Fiber handler that checks whether the authenticated admin
// has the given resource:action permission. Super admins bypass all permission checks.
func RequirePermission(resource, action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("admin_claims").(*AdminClaims)
		if !ok || claims == nil {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: "Unauthorized", Error: "missing admin claims",
			})
		}
		if claims.IsSuperAdmin {
			return c.Next()
		}
		perm := resource + ":" + action
		for _, p := range claims.Permissions {
			if p == perm {
				return c.Next()
			}
		}
		return c.Status(http.StatusForbidden).JSON(common.Response{
			Status: "Forbidden", Error: "insufficient permissions",
		})
	}
}

// claimsFromCtx extracts AdminClaims stored by adminJWTAuth middleware.
func claimsFromCtx(c fiber.Ctx) *AdminClaims {
	claims, _ := c.Locals("admin_claims").(*AdminClaims)
	return claims
}

// adminIDFromCtx returns the admin UUID from the JWT subject claim.
func adminIDFromCtx(c fiber.Ctx) (uuid.UUID, bool) {
	claims := claimsFromCtx(c)
	if claims == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(claims.Subject)
	return id, err == nil
}

// isSuperAdmin reports whether the request was made by a super admin.
func isSuperAdmin(c fiber.Ctx) bool {
	claims := claimsFromCtx(c)
	return claims != nil && claims.IsSuperAdmin
}

package middlewares

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/utils"
)

// OptionalAuthentication parses the JWT token if present and sets UserID/Username in context.
// Unlike Authentication, it does NOT abort on missing or invalid tokens — the handler
// decides whether to require auth.
func OptionalAuthentication(publicKey interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader(constraints.HeaderAuthorization)
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != constraints.TokenTypeBearer {
			c.Next()
			return
		}

		token, err := jwt.ParseWithClaims(parts[1], &utils.Claims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return publicKey, nil
		})

		if err == nil && token.Valid {
			if claims, ok := token.Claims.(*utils.Claims); ok {
				ctx := c.Request.Context()
				ctx = context.WithValue(ctx, constraints.ContextKeyClaims, claims)
				ctx = context.WithValue(ctx, constraints.ContextKeyUserID, claims.UserID)
				ctx = context.WithValue(ctx, constraints.ContextKeyUsername, claims.Username)
				c.Request = c.Request.WithContext(ctx)
			}
		}

		c.Next()
	}
}

// Authentication middleware validates the JWT token and sets UserID + Username in the context.
func Authentication(publicKey interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader(constraints.HeaderAuthorization)
		if authHeader == "" {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "missing authorization header", nil))
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != constraints.TokenTypeBearer {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "invalid authorization header format", nil))
			c.Abort()
			return
		}

		tokenString := parts[1]

		// Parse token
		token, err := jwt.ParseWithClaims(tokenString, &utils.Claims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return publicKey, nil
		})

		if err != nil || !token.Valid {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "invalid or expired token", nil))
			c.Abort()
			return
		}

		if claims, ok := token.Claims.(*utils.Claims); ok {
			ctx := c.Request.Context()
			ctx = context.WithValue(ctx, constraints.ContextKeyClaims, claims)
			ctx = context.WithValue(ctx, constraints.ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, constraints.ContextKeyUsername, claims.Username)
			c.Request = c.Request.WithContext(ctx)
		} else {
			response.ErrorResponse(c, apperr.CodeUnauthorized, apperr.New(apperr.CodeUnauthorized, "invalid token claims", nil))
			c.Abort()
			return
		}

		c.Next()
	}
}

package middlewares

import (
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
)

// Compose chains middlewares sequentially, then runs the final handler.
// Stops immediately if any handler calls c.Abort().
//
// Usage:
//
//	r.GET("/path", middlewares.Compose(
//	    middlewares.RateLimit(cfg),
//	)(myHandler))
func Compose(mws ...gin.HandlerFunc) func(gin.HandlerFunc) gin.HandlerFunc {
	return func(final gin.HandlerFunc) gin.HandlerFunc {
		chain := make(gin.HandlersChain, 0, len(mws)+1)
		chain = append(chain, mws...)
		chain = append(chain, final)
		return func(c *gin.Context) {
			for _, h := range chain {
				if c.IsAborted() {
					return
				}
				h(c)
			}
		}
	}
}

// ComposeHandlers combines multiple handlers into one slice for Gin route registration.
//
// Usage:
//
//	r.GET("/path", middlewares.ComposeHandlers(
//	    middlewares.Authentication(key),
//	    myHandler,
//	)...)
func ComposeHandlers(handlers ...gin.HandlerFunc) []gin.HandlerFunc {
	return handlers
}

// Parallel runs all enricher handlers concurrently and waits for all to finish.
// Auto-initializes a RequestStore in gin context for thread-safe data sharing.
// If any enricher calls middlewares.Abort(c, err), the request is aborted after
// all goroutines complete (no mid-flight cancellation).
//
// Usage:
//
//	users.GET("/profile",
//	    middlewares.Parallel(
//	        h.withStats,
//	        h.withTraits,
//	    ),
//	    h.GetProfile,
//	)
func Parallel(handlers ...gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		getOrInitStore(c)

		var wg sync.WaitGroup
		for _, h := range handlers {
			wg.Add(1)
			go func(fn gin.HandlerFunc) {
				defer wg.Done()
				fn(c)
			}(h)
		}
		wg.Wait()

		// Check abort signal set by enrichers (sequential — safe to read now)
		if err, ok := AbortError(c); ok {
			response.ErrorResponse(c, apperr.CodeInternalServer, err)
			c.Abort()
		}
	}
}

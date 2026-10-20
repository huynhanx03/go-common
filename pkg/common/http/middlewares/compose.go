package middlewares

import "github.com/gin-gonic/gin"

// Compose returns a normal Gin handler chain in declared order. Register the
// result with ... so Gin, rather than a manual loop, owns c.Next semantics.
func Compose(middlewares ...gin.HandlerFunc) func(gin.HandlerFunc) gin.HandlersChain {
	copied := append(gin.HandlersChain(nil), middlewares...)
	return func(final gin.HandlerFunc) gin.HandlersChain {
		chain := make(gin.HandlersChain, 0, len(copied)+1)
		chain = append(chain, copied...)
		chain = append(chain, final)
		return chain
	}
}

// ComposeHandlers copies a normal Gin handler chain in declared order.
func ComposeHandlers(handlers ...gin.HandlerFunc) gin.HandlersChain {
	return append(gin.HandlersChain(nil), handlers...)
}

package middlewares

import "github.com/gin-gonic/gin"

// CORSMiddleware handles Cross-Origin Resource Sharing (CORS) settings.
func CORSMiddleware(ctx *gin.Context) {
	method := ctx.Request.Method

	ctx.Header("Access-Control-Allow-Origin", ctx.Request.Header.Get("Origin"))
	ctx.Header("Access-Control-Allow-Credentials", "true")
	ctx.Header("Access-Control-Allow-Headers", "Content-Type, Access-Control-Allow-Headers, Authorization, X-Requested-With")
	ctx.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")

	if method == "OPTIONS" || method == "HEAD" {
		ctx.AbortWithStatus(204)
		return
	}

	ctx.Next()
}

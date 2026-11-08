package catalog

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

var (
	drainEntered = make(chan struct{})
	drainRelease = make(chan struct{})
	releaseOnce  sync.Once
)

func (module *Module) registerDrain(group *gin.RouterGroup) {
	group.GET("/_test/drain", func(context *gin.Context) {
		close(drainEntered)
		<-drainRelease
		context.Status(http.StatusNoContent)
	})
}

func DrainEntered() <-chan struct{} {
	return drainEntered
}

func ReleaseDrain() {
	releaseOnce.Do(func() { close(drainRelease) })
}

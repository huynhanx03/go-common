package temporal

import (
	"context"
	"fmt"

	sdklog "go.temporal.io/sdk/log"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
)

type sdkLogger struct {
	logger *logger.Logger
}

func newSDKLogger(log *logger.Logger) sdklog.Logger { return &sdkLogger{logger: log} }

func (l *sdkLogger) Debug(message string, keyValues ...any) {
	l.logger.Debug(context.Background(), message, sdkFields(keyValues)...)
}

func (l *sdkLogger) Info(message string, keyValues ...any) {
	l.logger.Info(context.Background(), message, sdkFields(keyValues)...)
}

func (l *sdkLogger) Warn(message string, keyValues ...any) {
	l.logger.Warn(context.Background(), message, sdkFields(keyValues)...)
}

func (l *sdkLogger) Error(message string, keyValues ...any) {
	l.logger.Error(context.Background(), message, sdkFields(keyValues)...)
}

func sdkFields(keyValues []any) []zap.Field {
	fields := make([]zap.Field, 0, len(keyValues)/2)
	for index := 0; index+1 < len(keyValues); index += 2 {
		fields = append(fields, zap.Any(fmt.Sprint(keyValues[index]), keyValues[index+1]))
	}
	return fields
}

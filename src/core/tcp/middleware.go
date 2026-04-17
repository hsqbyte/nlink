package tcp

import (
	"fmt"
	"runtime/debug"

	"github.com/fastgox/utils/logger"
)

// Chain 将中间件链和最终handler组装为一个handler
func Chain(middlewares []Middleware, handler Handler) Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// Recovery panic恢复中间件
func Recovery() Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) error {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("[TCP Recovery] connID=%s cmd=%s panic: %v\n%s",
						ctx.ConnID, ctx.Msg.Cmd, r, debug.Stack())
					if err := ctx.Error(500, fmt.Sprintf("内部错误: %v", r)); err != nil {
						logger.Error("[TCP Recovery] 发送错误响应失败: connID=%s err=%v", ctx.ConnID, err)
					}
				}
			}()
			return next(ctx)
		}
	}
}

// Logger 请求日志中间件
func Logger() Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) error {
			logger.Info("[TCP] connID=%s cmd=%s seq=%s", ctx.ConnID, ctx.Msg.Cmd, ctx.Msg.Seq)
			err := next(ctx)
			if err != nil {
				logger.Error("[TCP] connID=%s cmd=%s error: %v", ctx.ConnID, ctx.Msg.Cmd, err)
			}
			return err
		}
	}
}

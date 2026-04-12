package tcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/panjf2000/gnet/v2"
)

// Router TCP路由注册表
type Router struct {
	handlers   map[string]Handler
	middleware []Middleware
	mu         sync.RWMutex
}

// NewRouter 创建TCP路由
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]Handler),
	}
}

// Use 注册全局中间件
func (r *Router) Use(mws ...Middleware) {
	r.middleware = append(r.middleware, mws...)
}

// Handle 注册命令处理器
func (r *Router) Handle(cmd string, handler Handler) {
	r.mu.Lock()
	r.handlers[cmd] = handler
	r.mu.Unlock()
}

// Dispatch 分发消息到对应handler
func (r *Router) Dispatch(ctx *Context) error {
	r.mu.RLock()
	handler, ok := r.handlers[ctx.Msg.Cmd]
	r.mu.RUnlock()

	if !ok {
		return ctx.Error(404, fmt.Sprintf("未知命令: %s", ctx.Msg.Cmd))
	}

	// 组装中间件链
	final := Chain(r.middleware, handler)
	return final(ctx)
}

// Server TCP服务器，封装 gnet 引擎
type Server struct {
	gnet.BuiltinEventEngine

	addr             string
	codec            *Codec
	conns            *ConnManager
	router           *Router
	eng              gnet.Engine
	options          []gnet.Option
	heartbeatTimeout time.Duration // 心跳超时，0表示不启用

	// OnConnect 新连接回调
	OnConnect func(connID string, conn gnet.Conn)
	// OnDisconnect 断连回调
	OnDisconnect func(connID string)
}

// NewServer 创建TCP服务器
func NewServer(port int, maxMessageSize int, heartbeatTimeout int, router *Router, opts ...gnet.Option) *Server {
	return &Server{
		addr:             fmt.Sprintf("tcp://:%d", port),
		codec:            NewCodec(maxMessageSize),
		conns:            NewConnManager(),
		router:           router,
		options:          opts,
		heartbeatTimeout: time.Duration(heartbeatTimeout) * time.Second,
	}
}

// ConnManager 获取连接管理器
func (s *Server) ConnManager() *ConnManager {
	return s.conns
}

// Codec 获取编解码器
func (s *Server) Codec() *Codec {
	return s.codec
}

// Start 启动TCP服务器（阻塞）
func (s *Server) Start() error {
	logger.Info("[TCP] 启动TCP服务器: %s", s.addr)
	return gnet.Run(s, s.addr, s.options...)
}

// Stop 关闭TCP服务器
func (s *Server) Stop() error {
	logger.Info("[TCP] 正在关闭TCP服务器...")
	return s.eng.Stop(context.Background())
}

// ===== gnet 事件回调 =====

func (s *Server) OnBoot(eng gnet.Engine) gnet.Action {
	s.eng = eng
	logger.Info("[TCP] 服务器已启动, 监听: %s", s.addr)
	return gnet.None
}

func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	connID := s.conns.Add(c)
	if s.heartbeatTimeout > 0 {
		c.SetDeadline(time.Now().Add(s.heartbeatTimeout))
	}
	logger.Info("[TCP] 新连接: connID=%s remote=%s (在线: %d)", connID, c.RemoteAddr(), s.conns.Count())
	if s.OnConnect != nil {
		s.OnConnect(connID, c)
	}
	return nil, gnet.None
}

func (s *Server) OnClose(c gnet.Conn, err error) gnet.Action {
	connID := ConnIDFromConn(c)
	s.conns.Remove(connID)
	if s.OnDisconnect != nil {
		s.OnDisconnect(connID)
	}
	if err != nil {
		logger.Info("[TCP] 连接关闭: connID=%s err=%v (在线: %d)", connID, err, s.conns.Count())
	} else {
		logger.Info("[TCP] 连接关闭: connID=%s (在线: %d)", connID, s.conns.Count())
	}
	return gnet.None
}

func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	connID := ConnIDFromConn(c)

	for {
		msg, err := s.codec.Decode(c)
		if err != nil {
			logger.Error("[TCP] 解码失败: connID=%s err=%v", connID, err)
			return gnet.Close // 协议错误，关闭连接
		}
		if msg == nil {
			break // 数据不足，等待下次
		}

		// 收到任何消息，刷新心跳计时
		if s.heartbeatTimeout > 0 {
			c.SetDeadline(time.Now().Add(s.heartbeatTimeout))
		}

		// 内置 ping 命令，直接回复 pong，不走路由
		if msg.Cmd == "ping" {
			resp := &Response{Cmd: "pong", Seq: msg.Seq, Code: 200, Message: "pong"}
			if buf, err := s.codec.Encode(resp); err == nil {
				c.Write(buf)
			}
			continue
		}

		// 创建上下文并分发
		ctx := NewContext(c, connID, msg, s.codec)
		if err := s.router.Dispatch(ctx); err != nil {
			logger.Error("[TCP] 处理失败: connID=%s cmd=%s err=%v", connID, msg.Cmd, err)
		}
	}

	return gnet.None
}

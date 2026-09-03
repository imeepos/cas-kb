package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/imeepos/cas-kb/internal/embed"
	"github.com/imeepos/cas-kb/internal/server"
	"github.com/imeepos/cas-kb/internal/store"
)

// defaultServeAddr 是 kb serve 的默认监听地址:只绑回环,不对外暴露。
// 跨机消费走 SSH 端口转发或反向代理(DESIGN §8.5 安全边界)。
const defaultServeAddr = "127.0.0.1:8787"

// shutdownGrace 是优雅退出的排空窗口:停收新请求,等待在途请求完成。
const shutdownGrace = 5 * time.Second

// serveTokenEnv 是写入令牌的环境变量名;--token 旗标优先于它。
// 令牌只从内存比较,绝不写日志/回显。
const serveTokenEnv = "KB_SERVE_TOKEN"

// cmdServe 处理 kb serve:只读 HTTP API + 可选写入型(配置令牌后)。
// 用法:kb serve [--addr 127.0.0.1:8787] [-p 项目] [--token <值>];
// KB_DSN/KB_SERVE_TOKEN 正常生效(两后端都可 serve);启动时打印后端与项目作用域;
// SIGINT/SIGTERM 或 ctx 取消时优雅退出。
func cmdServe(ctx context.Context, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--addr": true, "--token": true})
	if err != nil {
		return err
	}
	addr := f.get("--addr", defaultServeAddr)
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("serve: --addr 不能为空")
	}
	token := f.get("--token", os.Getenv(serveTokenEnv))
	dsn := effectiveDSN()
	// 语义检索(M6-B/M6-C):serve 进程同样读 KB_EMBED_*(KB_EMBED_PROVIDER
	// 缺省 ollama、可选 openai);未配置不拦启动(其余端点零影响),
	// mode=hybrid 请求届时返回 409 + 配置指引。
	var emb embed.Embedder
	if e, err := embed.ProviderFromEnv(); err == nil {
		emb = e
	}
	p, err := startServe(ctx, addr, server.Options{DSN: dsn, Project: projectName(), Branch: branchName(), Token: token, Embedder: emb})
	if err != nil {
		return err
	}
	name, target := store.DescribeBackend(dsn)
	if token == "" {
		fmt.Printf("kb serve 只读 HTTP API(未配置写入令牌,纯只读)\n")
	} else {
		fmt.Printf("kb serve 写入型 HTTP API(已配置写入令牌,写端点需 Bearer 鉴权)\n")
	}
	if emb == nil {
		fmt.Printf("语义检索未启用(未配置 KB_EMBED_*;mode=hybrid 请求将返回 409 与配置指引)\n")
	} else {
		fmt.Printf("语义检索已启用(mode=hybrid;嵌入模型 %s)\n", emb.Model())
	}
	fmt.Printf("后端 %s(%s)\n项目作用域 %s(分支 %s)\n监听 http://%s\n", name, target, p.Project(), p.Branch(), p.Addr().String())
	fmt.Printf("Ctrl-C 优雅退出\n")
	return p.wait(ctx)
}

// serveProc 是一个已启动的 serve 实例(进程内视图),命令路径与测试共用。
type serveProc struct {
	srv *http.Server
	s   *server.Server
	ln  net.Listener
	// done 在 Serve 返回(出错或 Shutdown 完成)后收到其返回值。
	done chan error
}

// startServe 打开存储、监听 addr 并开始服务;端口给 0 时由内核分配(测试用)。
func startServe(ctx context.Context, addr string, opts server.Options) (*serveProc, error) {
	s, err := server.New(ctx, opts)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("serve: 监听 %s 失败: %w", addr, err)
	}
	p := &serveProc{srv: &http.Server{Handler: s.Handler()}, s: s, ln: ln, done: make(chan error, 1)}
	go func() { p.done <- p.srv.Serve(ln) }()
	return p, nil
}

// Addr 返回实际监听地址(:0 端口时为内核分配的最终地址)。
func (p *serveProc) Addr() net.Addr { return p.ln.Addr() }

// Project 返回生效的项目作用域。
func (p *serveProc) Project() string { return p.s.Project() }

// Branch 返回默认分支名。
func (p *serveProc) Branch() string { return p.s.Branch() }

// wait 阻塞直到收到 SIGINT/SIGTERM、ctx 取消或服务自身出错,
// 然后优雅关闭(停收新请求、排空在途请求)并释放存储连接。
func (p *serveProc) wait(ctx context.Context) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case <-ctx.Done():
	case <-sigCh:
	case err := <-p.done: // 服务自身退出(如监听失败),直接收尾
		_ = p.s.Close()
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = p.srv.Shutdown(shutdownCtx)
	<-p.done
	return p.s.Close()
}

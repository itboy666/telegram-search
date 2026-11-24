package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"starindex/server/internal/app"
)

func main() {
	command := "all"
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("starindex", flag.ExitOnError)
	root := fs.String("root", ".", "project root holding .env and public/")
	address := fs.String("addr", "127.0.0.1:8088", "HTTP listen address")
	path := fs.String("file", "", "JSON or CSV resource file")
	source := fs.String("source", "go_import", "provenance source label")
	apply := fs.Bool("apply", false, "insert importer rows (default dry run)")
	_ = fs.Parse(args)
	abs, e := filepath.Abs(*root)
	if e != nil {
		log.Fatal("项目路径无效")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command == "all" || command == "serve" {
		runWeb(ctx, cancel, abs, *address, command)
		return
	}
	a, e := app.New(abs)
	if e != nil {
		log.Fatal("配置或数据库不可用；新部署请先运行 all 并访问域名完成安装。")
	}
	defer a.DB.Close()
	switch command {
	case "migrate":
		if e = a.Migrate(ctx); e != nil {
			log.Fatal(e)
		}
		log.Print("Go 数据库迁移完成，现有业务数据已保留。")
		return
	case "import":
		report, e := a.ImportResources(ctx, *path, *source, *apply)
		if e != nil {
			log.Fatal(e)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return
	case "maintain":
		report, e := a.Maintain()
		if e != nil {
			log.Fatal(e)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return
	case "bot":
		if e = a.RunBot(ctx); e != nil {
			log.Fatal(e)
		}
		return
	case "commerce":
		a.RunCommerce(ctx)
		return
	default:
		log.Fatal("支持命令：all / serve / bot / commerce / migrate / import / maintain")
	}
}

func runWeb(ctx context.Context, cancel context.CancelFunc, root, address, command string) {
	b, e := app.NewBootstrap(root)
	if e != nil {
		log.Fatal(e)
	}
	defer b.Close()
	listener, e := net.Listen("tcp", address)
	if e != nil {
		log.Fatal("监听地址已被占用，请先停止旧服务。")
	}
	srv := &http.Server{Addr: address, Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	var wg sync.WaitGroup
	if command == "all" {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a := b.WaitReady(ctx)
			if a == nil {
				return
			}
			if e := a.RunBot(ctx); e != nil {
				log.Print(e)
				cancel()
			}
		}()
		go func() {
			defer wg.Done()
			if a := b.WaitReady(ctx); a != nil {
				a.RunCommerce(ctx)
			}
		}()
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("StarIndex Go 已启动：http://%s（%s）", address, command)
	if !b.Installed() {
		log.Print("首次部署：访问域名进入安装向导，安装码保存在项目根目录 .install-token 文件。")
	}
	if e = srv.Serve(listener); e != nil && e != http.ErrServerClosed {
		log.Print("Web 服务意外停止")
		cancel()
	}
	cancel()
	wg.Wait()
	<-shutdownDone
}

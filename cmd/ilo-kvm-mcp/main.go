package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ilo-kvm/internal/console"
	"ilo-kvm/internal/mcpserver"
)

type options struct {
	transport      string
	listen         string
	endpoint       string
	isoRoot        string
	sessionTTL     time.Duration
	connectTimeout time.Duration
}

const maximumMCPRequestBytes = 1 << 20

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("iLO-KVM-mcp: %v", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("iLO-KVM-mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	opts := options{}
	flags.StringVar(&opts.transport, "transport", "stdio", "MCP transport: stdio or http")
	flags.StringVar(&opts.listen, "listen", "127.0.0.1:8765", "HTTP listen address (loopback only)")
	flags.StringVar(&opts.endpoint, "endpoint", "/mcp", "HTTP MCP endpoint")
	flags.StringVar(&opts.isoRoot, "iso-root", "", "directory containing ISO files allowed for virtual media; empty disables ISO mounting")
	flags.DurationVar(&opts.sessionTTL, "session-ttl", console.DefaultSessionTTL, "idle lifetime of an open iLO console handle")
	flags.DurationVar(&opts.connectTimeout, "connect-timeout", console.DefaultConnectTimeout, "timeout for iLO login and KVM setup")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if opts.sessionTTL <= 0 {
		return errors.New("session-ttl must be greater than zero")
	}
	if opts.connectTimeout <= 0 {
		return errors.New("connect-timeout must be greater than zero")
	}
	isoRoot, err := console.NewISORoot(opts.isoRoot)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(os.Stderr, "iLO-KVM-mcp: ", log.LstdFlags|log.LUTC)
	manager := console.NewManager(ctx, console.ManagerOptions{
		SessionTTL:     opts.sessionTTL,
		ConnectTimeout: opts.connectTimeout,
		ISORoot:        isoRoot,
		Logger:         logger.Printf,
	})
	defer manager.Close()
	server := mcpserver.New(manager)

	switch strings.ToLower(strings.TrimSpace(opts.transport)) {
	case "stdio":
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return runHTTP(ctx, opts, server, logger)
	default:
		return fmt.Errorf("transport must be %q or %q", "stdio", "http")
	}
}

func runHTTP(ctx context.Context, opts options, server *mcp.Server, logger *log.Logger) error {
	if err := validateLoopbackListen(opts.listen); err != nil {
		return err
	}
	if !strings.HasPrefix(opts.endpoint, "/") || strings.Contains(opts.endpoint, "?") || strings.Contains(opts.endpoint, "#") {
		return errors.New("endpoint must be an absolute HTTP path without query or fragment")
	}

	mux := http.NewServeMux()
	mux.Handle(opts.endpoint, http.MaxBytesHandler(newMCPHTTPHandler(server), maximumMCPRequestBytes))
	httpServer := &http.Server{
		Addr:              opts.listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return err
	}
	logger.Printf("stateless MCP HTTP listening address=%q endpoint=%q", listener.Addr().String(), opts.endpoint)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = httpServer.Shutdown(shutdownCtx)
			cancel()
		case <-done:
		}
	}()
	err = httpServer.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newMCPHTTPHandler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("HTTP transport may listen only on an explicit loopback address")
	}
	return nil
}

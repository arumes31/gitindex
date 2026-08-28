package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitindex/internal/cache"
	"gitindex/internal/config"
	"gitindex/internal/github"
	"gitindex/internal/server"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()
	if *healthcheck {
		runHealthcheck()
		return
	}

	cfg := config.Load()

	c := cache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	if err := c.Ping(ctx); err != nil {
		log.Printf("[warn] redis unreachable at %s (%v) — degraded mode (cache unavailable)", cfg.RedisAddr, err)
	} else {
		log.Printf("[ok] connected to redis at %s", cfg.RedisAddr)
	}
	cancel()

	appCtx, stop := context.WithCancel(context.Background())
	defer stop()

	gh := github.New(cfg, c)
	gh.StartBackgroundRefresh(appCtx)

	srv, err := server.New(cfg, gh, c, Templates(), StaticFS())
	if err != nil {
		log.Fatalf("server init: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("gitindex listening on :%s (user: %s, readmeTTL: %s, repoListTTL: %s, rateLimit: %t/%d per %s, trustProxy: %t)", cfg.Port, cfg.GitHubUser, cfg.ReadmeTTL, cfg.RepoListTTL, cfg.RateLimitEnabled, cfg.RateLimitRequests, cfg.RateLimitWindow, cfg.TrustProxy)

	shutdownCtx, shutdownSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer shutdownSignal()
	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("[error] graceful shutdown: %v", err)
		}
	}()

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}
	if err := c.Close(); err != nil {
		log.Printf("[warn] closing redis client: %v", err)
	}
}

// runHealthcheck is used by the container HEALTHCHECK in a distroless image that
// has no shell or curl. It exits 0 if the local server answers, 1 otherwise.
func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "6541"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	// #nosec G704 -- fixed loopback host; port is operator-set, not attacker-controlled
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		fmt.Println("unhealthy")
		os.Exit(1)
	}
	_ = resp.Body.Close()
	fmt.Println("ok")
}

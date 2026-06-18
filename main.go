package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
	}
	log.Printf("gitindex listening on :%s (user: %s, readmeTTL: %s, repoListTTL: %s, rateLimit: %t/%d per %s, trustProxy: %t)", cfg.Port, cfg.GitHubUser, cfg.ReadmeTTL, cfg.RepoListTTL, cfg.RateLimitEnabled, cfg.RateLimitRequests, cfg.RateLimitWindow, cfg.TrustProxy)
	log.Fatal(httpSrv.ListenAndServe())
}

// runHealthcheck is used by the container HEALTHCHECK in a distroless image that
// has no shell or curl. It exits 0 if the local server answers, 1 otherwise.
func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "6541"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		fmt.Println("unhealthy")
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println("ok")
}

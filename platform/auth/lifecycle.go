package main

import (
	"context"
	"net/http"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

var draining atomic.Bool

func drainSeconds() time.Duration {
	n := 25
	if v := os.Getenv("PF_DRAIN_SECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return time.Duration(n) * time.Second
}

func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if draining.Load() {
		writeErr(w, http.StatusServiceUnavailable, "draining")
		return
	}
	s.handleHealthz(w, r)
}

func serveHTTP(handler http.Handler) {
	srv := &http.Server{Addr: ":8080", Handler: handler}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	draining.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), drainSeconds())
	defer cancel()
	_ = srv.Shutdown(ctx)
}

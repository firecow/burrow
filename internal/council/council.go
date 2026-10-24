// Package council provides the council HTTP server and state management.
package council

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/state"
	"github.com/spf13/cobra"
)

const (
	defaultPort             = 8080
	readHeaderTimeout       = 5 * time.Second
	gracefulShutdownTimeout = 5 * time.Second
	warmupDuration          = 3 * time.Second
)

// Command returns the cobra command for starting the council server.
func Command() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "council",
		Short: "Start council",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), port)
		},
		Aliases:                nil,
		SuggestFor:             nil,
		GroupID:                "",
		Long:                   "",
		Example:                "",
		ValidArgs:              nil,
		ValidArgsFunction:      nil,
		Args:                   nil,
		ArgAliases:             nil,
		BashCompletionFunction: "",
		Deprecated:             "",
		Annotations:            nil,
		Version:                "",
		PersistentPreRun:       nil,
		PersistentPreRunE:      nil,
		PreRun:                 nil,
		PreRunE:                nil,
		Run:                    nil,
		PostRun:                nil,
		PostRunE:               nil,
		PersistentPostRun:      nil,
		PersistentPostRunE:     nil,
		FParseErrWhitelist: cobra.FParseErrWhitelist{
			UnknownFlags: false,
		},
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd:   false,
			DisableNoDescFlag:   false,
			DisableDescriptions: false,
			HiddenDefaultCmd:    false,
		},
		TraverseChildren:           false,
		Hidden:                     false,
		SilenceErrors:              false,
		SilenceUsage:               false,
		DisableFlagParsing:         false,
		DisableAutoGenTag:          false,
		DisableFlagsInUseLine:      false,
		DisableSuggestions:         false,
		SuggestionsMinimumDistance: 0,
	}

	cmd.Flags().IntVar(&port, "port", defaultPort, "Webserver listening port")

	return cmd
}

func run(ctx context.Context, port int) error {
	currentState := &state.State{
		Revision: int(time.Now().UnixMilli()),
		Services: []state.Service{},
		Kings:    []state.King{},
		Lings:    []state.Ling{},
	}

	var stateMutex sync.RWMutex

	hub := NewWSHub()
	warmup := NewWarmup(warmupDuration)
	server := newServer(ctx, port, currentState, &stateMutex, hub, warmup)
	cleanerStop := make(chan struct{})

	go StartCleaner(ctx, currentState, &stateMutex, hub, warmup, cleanerStop)

	go func() {
		warmup.wait(ctx)

		stateMutex.Lock()
		Provision(currentState)

		currentState.Revision++
		stateMutex.Unlock()

		slog.Info("Warmup complete, serving state")
		hub.Broadcast(ctx)
	}()

	go func() {
		<-ctx.Done()
		slog.Info("Shutdown sequence initiated")
		close(cleanerStop)
		hub.closeAll()

		shutdownCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("Ready", "port", port)

	err := server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

func newServer(
	ctx context.Context,
	port int,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	warmup *Warmup,
) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", HandleGetState(currentState, stateMutex, warmup))
	mux.HandleFunc("PUT /king", HandlePutKing(currentState, stateMutex, hub, warmup))
	mux.HandleFunc("PUT /ling", HandlePutLing(currentState, stateMutex, hub, warmup))
	mux.HandleFunc("/ws", hub.handleWebSocket)

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
		DisableGeneralOptionsHandler: false,
		TLSConfig:                    nil,
		ReadTimeout:                  0,
		WriteTimeout:                 0,
		IdleTimeout:                  0,
		MaxHeaderBytes:               0,
		TLSNextProto:                 nil,
		ConnState:                    nil,
		ErrorLog:                     nil,
		ConnContext:                  nil,
		HTTP2:                        nil,
		Protocols:                    nil,
	}
}

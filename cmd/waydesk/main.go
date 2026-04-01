// WayDesk — Low-latency remote desktop for Wayland.
//
// This entry point demonstrates the initial XDG Desktop Portal handshake:
// requesting screen sharing permission and obtaining PipeWire stream metadata.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ferbman/WayDesk/internal/capture"
	"github.com/ferbman/WayDesk/internal/input"
	"github.com/ferbman/WayDesk/internal/net"
	"github.com/ferbman/WayDesk/internal/portal"
)

func main() {
	// ── Flags ──────────────────────────────────────────────────────────
	logFormat := flag.String("log-format", "text", "Log format: text or json")
	timeout := flag.Duration("timeout", 60*time.Second, "Timeout for portal interaction")
	flag.Parse()

	// ── Logger ─────────────────────────────────────────────────────────
	var handler slog.Handler
	switch *logFormat {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	default:
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	if err := run(logger, *timeout); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, timeout time.Duration) error {
	// ── Context with cancellation on SIGINT/SIGTERM ────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// ── Portal Client ──────────────────────────────────────────────────
	client, err := portal.NewClient(ctx, logger.With("component", "portal"))
	if err != nil {
		return fmt.Errorf("create portal client: %w", err)
	}
	defer client.Close()

	// ── ScreenCast Flow ────────────────────────────────────────────────
	portalCtx, portalCancel := context.WithTimeout(ctx, timeout)
	defer portalCancel()

	// Step 1: Create session
	session, err := client.CreateScreenCastSession(portalCtx)
	if err != nil {
		return fmt.Errorf("create screencast session: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			logger.Warn("failed to close session", "error", closeErr)
		}
	}()

	// Step 2: Select sources (monitor with embedded cursor)
	if err := session.SelectSources(portalCtx, portal.SourceMonitor, portal.CursorEmbedded); err != nil {
		return fmt.Errorf("select sources: %w", err)
	}

	// Step 3: Start the stream
	if err := session.Start(portalCtx); err != nil {
		return fmt.Errorf("start screencast: %w", err)
	}

	// Step 4: Get PipeWire FD
	if err := session.OpenPipeWireRemote(portalCtx); err != nil {
		return fmt.Errorf("open pipewire remote: %w", err)
	}

	// ── Phase 2 & 3: Capture & WebRTC ──────────────────────────────────
	// Note: We only support the first stream for now.
	if len(session.Streams) == 0 {
		return fmt.Errorf("no streams discovered from portal")
	}
	stream := session.Streams[0]

	// ── Phase 4: Virtual Input ─────────────────────────────────────────
	inputCtrl, err := input.NewController(logger.With("component", "input"))
	if err != nil {
		logger.Error("failed to create virtual input devices, input simulation disabled", "error", err)
	} else {
		defer inputCtrl.Close()
	}

	// 1. Initialize WebRTC Session
	webrtcSess, err := net.NewWebRTCSession(inputCtrl, logger.With("component", "webrtc"))
	if err != nil {
		return fmt.Errorf("create webrtc session: %w", err)
	}
	defer webrtcSess.Close()

	// 2. Initialize GStreamer Pipeline
	pipeline, err := capture.NewPipeline(session.PipeWireFD, stream.NodeID, logger.With("component", "capture"))
	if err != nil {
		return fmt.Errorf("create capture pipeline: %w", err)
	}

	// 3. Link pipeline output -> WebRTC track
	pipeline.SetOnSample(func(data []byte) {
		if err := webrtcSess.WriteVideo(data); err != nil {
			logger.Debug("failed to write video sample", "error", err)
		}
	})

	// 4. Start the capture pipeline
	if err := pipeline.Start(); err != nil {
		return fmt.Errorf("start capture pipeline: %w", err)
	}
	defer pipeline.Stop()

	// 5. Start Signaling Server
	sigServer := net.NewSignalingServer(8080, logger.With("component", "signaling"))
	
	// When the signaling server gets an offer, it will use our single global WebRTC session
	// (Only one concurrent connection supported in this initial version)
	sigServer.OnSessionRequest = func() (*net.WebRTCSession, error) {
		return webrtcSess, nil
	}

	go func() {
		if err := sigServer.Start(); err != nil {
			logger.Error("signaling server failed", "error", err)
		}
	}()

	// ── Summary ────────────────────────────────────────────────────────
	logger.Info("screencast ready",
		"session", session.SessionHandle(),
		"pipewire_fd", session.PipeWireFD,
		"stream_count", len(session.Streams),
	)

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  WayDesk — Screen sharing active")
	fmt.Printf("  PipeWire FD: %d\n", session.PipeWireFD)
	fmt.Printf("  Stream: node_id=%d size=%dx%d\n", stream.NodeID, stream.Size[0], stream.Size[1])
	fmt.Println("  WebRTC Web Player is running at:")
	fmt.Println("  http://<BİLGİSAYAR_LOKAL_IP>:8080")
	fmt.Println("  Press Ctrl+C to stop.")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// ── Wait for shutdown signal ───────────────────────────────────────
	<-ctx.Done()
	logger.Info("shutting down gracefully")
	return nil
}

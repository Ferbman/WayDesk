package net

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ferbman/WayDesk/internal/input"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// WebRTCSession manages a single peer connection for streaming desktop video.
type WebRTCSession struct {
	pc         *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticSample
	inputCtrl  *input.Controller
	logger     *slog.Logger
}

// InputMessage describes a remote control action coming from the browser.
type InputMessage struct {
	Type   string  `json:"type"`             // mousemove, mouseclick, mousescroll, keydown, keyup
	X      float64 `json:"cx,omitempty"`     // 0.0 - 1.0 (normalized relative to screen bounds)
	Y      float64 `json:"cy,omitempty"`     // 0.0 - 1.0
	Code   string  `json:"code,omitempty"`   // Javascript KeyboardEvent.code
	Button int     `json:"button,omitempty"` // 0=left, 1=middle, 2=right
	Down   bool    `json:"down,omitempty"`   // true for press, false for release
	DeltaY int32   `json:"deltaY,omitempty"` // Wheel scroll direction
}

// NewWebRTCSession initializes a WebRTC PeerConnection for video.
func NewWebRTCSession(inputCtrl *input.Controller, logger *slog.Logger) (*WebRTCSession, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}

	// Logging connection state changes
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		logger.Info("webrtc connection state changed", "state", s.String())
	})

	// Setup DataChannel support for remote control input
	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		logger.Info("datachannel opened", "label", d.Label())
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			if !msg.IsString || inputCtrl == nil {
				return
			}
			var inMsg InputMessage
			if err := json.Unmarshal(msg.Data, &inMsg); err != nil {
				logger.Debug("invalid input json", "error", err)
				return
			}

			// Route input message to kernel device controller
			switch inMsg.Type {
			case "mousemove":
				_ = inputCtrl.MoveRelative(inMsg.X, inMsg.Y)
			case "mouseclick":
				_ = inputCtrl.MouseButton(inMsg.Button, inMsg.Down)
			case "mousescroll":
				_ = inputCtrl.MouseWheel(inMsg.DeltaY)
			case "keydown":
				_ = inputCtrl.KeyPress(WebCodeToLinux[inMsg.Code], true)
			case "keyup":
				_ = inputCtrl.KeyPress(WebCodeToLinux[inMsg.Code], false)
			}
		})
	})

	// Create a video track for H.264 video
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"pion",
	)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("create video track: %w", err)
	}

	// Add track to peer connection
	rtpSender, err := pc.AddTrack(videoTrack)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("add track: %w", err)
	}

	// Read RTCP packets to prevent them from buffering infinitely
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(rtcpBuf); err != nil {
				return
			}
		}
	}()

	return &WebRTCSession{
		pc:         pc,
		videoTrack: videoTrack,
		logger:     logger,
	}, nil
}

// ProcessOffer takes an SDP offer from the client, sets it as the remote description,
// and returns the local SDP answer.
func (s *WebRTCSession) ProcessOffer(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if err := s.pc.SetRemoteDescription(offer); err != nil {
		return nil, fmt.Errorf("set remote description: %w", err)
	}

	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("create answer: %w", err)
	}

	// Gather ICE candidates
	gatherComplete := webrtc.GatheringCompletePromise(s.pc)

	if err := s.pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("set local description: %w", err)
	}

	// Wait for ICE gathering to complete before returning the answer
	<-gatherComplete

	return s.pc.LocalDescription(), nil
}

// WriteVideo writes a raw H.264 NAL unit sample into the WebRTC track.
func (s *WebRTCSession) WriteVideo(data []byte) error {
	// WebRTC TrackLocalStaticSample handles packetization (splitting NALUs into RTP packets)
	// under the hood when writing directly to it.
	
	// CRITICAL: We pass a fixed Duration of 16ms (~60FPS). 
	// Wayland emits Variable Framerate (VFR) by pausing frame delivery when the screen is idle.
	// If Duration is omitted, Pion attempts to compute RTP timestamps based on time.Now(), 
	// causing massive multi-second leaps. The browser jitter buffer sees these gaps and 
	// hoards frames for 6 seconds trying to smooth out the playback!
	// Forcing a fixed duration tricks the browser into playing frames immediately.
	return s.videoTrack.WriteSample(media.Sample{
		Data:     data,
		Duration: time.Millisecond * 16,
	})
}

// Close terminates the peer connection.
func (s *WebRTCSession) Close() error {
	s.logger.Info("closing webrtc session")
	return s.pc.Close()
}

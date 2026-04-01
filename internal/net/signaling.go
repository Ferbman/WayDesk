package net

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// SignalingServer serves the HTML player and handles SDP offer/answer exchange.
type SignalingServer struct {
	logger *slog.Logger
	port   int

	// OnSessionRequest is called when a client connects and requests a stream.
	// It should return a configured WebRTCSession. 
	OnSessionRequest func() (*WebRTCSession, error)
}

// NewSignalingServer creates a new local HTTP signaling server.
func NewSignalingServer(port int, logger *slog.Logger) *SignalingServer {
	return &SignalingServer{
		logger: logger,
		port:   port,
	}
}

// Start begins listening on the specified port. This function blocks.
func (s *SignalingServer) Start() error {
	http.HandleFunc("/", s.handleIndex)
	http.HandleFunc("/offer", s.handleOffer)

	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	s.logger.Info("starting signaling server", "address", addr)
	
	return http.ListenAndServe(addr, nil)
}

func (s *SignalingServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlPlayer))
}

func (s *SignalingServer) handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read SDP Offer from request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Error("failed to read offer", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var offer webrtc.SessionDescription
	if err := json.Unmarshal(body, &offer); err != nil {
		s.logger.Error("failed to decode offer", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	s.logger.Info("received WebRTC offer from client", "remote_addr", r.RemoteAddr)

	// Delegate session creation to the main app loop
	if s.OnSessionRequest == nil {
		http.Error(w, "Not configured", http.StatusInternalServerError)
		return
	}

	session, err := s.OnSessionRequest()
	if err != nil {
		s.logger.Error("failed to create session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Process the offer and generate an answer
	answer, err := session.ProcessOffer(offer)
	if err != nil {
		s.logger.Error("failed to process offer", "error", err)
		session.Close()
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	s.logger.Info("sending WebRTC answer to client")

	// Return the SDP answer to the client as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(answer); err != nil {
		s.logger.Error("failed to encode answer", "error", err)
		return
	}
}

// Simple HTML5 player with inline JavaScript to connect to the signaling server.
const htmlPlayer = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
    <title>WayDesk Desktop Viewer</title>
    <style>
        body, html {
            margin: 0; padding: 0;
            width: 100%; height: 100%;
            background-color: #000;
            display: flex; justify-content: center; align-items: center;
			overflow: hidden;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        }
        video {
            width: 100%; height: 100%;
            object-fit: contain;
        }
        #controls {
            position: absolute;
            background: rgba(0, 0, 0, 0.7);
            color: white;
            padding: 20px;
            border-radius: 8px;
            text-align: center;
        }
        button {
            background: #007bff; color: white;
            border: none; padding: 12px 24px;
            border-radius: 6px; font-size: 16px;
            cursor: pointer; margin-top: 10px;
        }
        button:hover { background: #0056b3; }
        .hidden { display: none !important; }
    </style>
</head>
<body>
    <div id="controls">
        <h2>WayDesk</h2>
        <p>Local remote desktop stream</p>
        <button id="connectBtn">Connect to Desktop</button>
        <p id="status"></p>
    </div>
    <video id="videoElement" autoplay playsinline class="hidden"></video>

    <script>
        const pc = new RTCPeerConnection({
            iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
        });
        
        const video = document.getElementById('videoElement');
        const connectBtn = document.getElementById('connectBtn');
        const controls = document.getElementById('controls');
        const status = document.getElementById('status');

        // Setup DataChannel for remote control
        const dataChannel = pc.createDataChannel("control");
        dataChannel.onopen = () => console.log("Data channel opened");
        
        function sendInput(msg) {
            if (dataChannel.readyState === "open") {
                dataChannel.send(JSON.stringify(msg));
            }
        }

        // Prevent context menu to allow right-click signaling
        window.addEventListener('contextmenu', e => e.preventDefault());

        let lastX = null;
        let lastY = null;
        let isDown = false;

        video.addEventListener('pointerdown', (e) => {
            isDown = true;
            lastX = e.clientX;
            lastY = e.clientY;
            video.setPointerCapture(e.pointerId);
            sendInput({ type: "mouseclick", button: e.button, down: true });
        });

        const releasePointer = (e) => {
            if (isDown) {
                isDown = false;
                lastX = null;
                lastY = null;
                video.releasePointerCapture(e.pointerId);
                sendInput({ type: "mouseclick", button: e.button || 0, down: false });
            }
        };

        video.addEventListener('pointerup', releasePointer);
        video.addEventListener('pointercancel', releasePointer);

        video.addEventListener('pointermove', (e) => {
            if (lastX !== null && lastY !== null) {
                const dx = e.clientX - lastX;
                const dy = e.clientY - lastY;
                if (dx !== 0 || dy !== 0) {
                    sendInput({ type: "mousemove", cx: dx, cy: dy });
                }
            }
            // For desktop, track hover movements. For mobile, it tracks drags.
            lastX = e.clientX;
            lastY = e.clientY;
        });

        // Clear hover tracking safely if mouse leaves
        video.addEventListener('pointerleave', () => { if(!isDown) { lastX = null; lastY = null; }});

        window.addEventListener('keydown', (e) => {
            if (document.activeElement === video || video.classList.contains('hidden') === false) {
                e.preventDefault();
                sendInput({ type: "keydown", code: e.code });
            }
        });

        window.addEventListener('keyup', (e) => {
            if (document.activeElement === video || video.classList.contains('hidden') === false) {
                e.preventDefault();
                sendInput({ type: "keyup", code: e.code });
            }
        });

        pc.ontrack = (event) => {
            console.log("Track received:", event.track.kind);
            if (event.track.kind === 'video') {
                video.srcObject = event.streams[0];
                video.classList.remove('hidden');
                controls.classList.add('hidden');
            }
        };

        pc.onconnectionstatechange = () => {
			console.log("Connection state:", pc.connectionState);
			status.innerText = "Status: " + pc.connectionState;
            if (pc.connectionState === 'disconnected' || pc.connectionState === 'failed') {
                controls.classList.remove('hidden');
                video.classList.add('hidden');
            }
        };

        connectBtn.onclick = async () => {
            connectBtn.disabled = true;
            status.innerText = "Connecting...";
            
            // WebRTC requires a transceiver to be added BEFORE creating an offer 
            // if we want to receive video but not send it.
            pc.addTransceiver('video', { direction: 'recvonly' });

            const offer = await pc.createOffer();
            await pc.setLocalDescription(offer);

            try {
                const response = await fetch('/offer', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(pc.localDescription)
                });

                if (!response.ok) throw new Error("Server rejected offer");

                const answer = await response.json();
                await pc.setRemoteDescription(new RTCSessionDescription(answer));
            } catch (err) {
                console.error(err);
                status.innerText = "Connection failed: " + err.message;
                connectBtn.disabled = false;
            }
        };
    </script>
</body>
</html>
`

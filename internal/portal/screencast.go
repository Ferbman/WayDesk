package portal

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	screenCastIface = "org.freedesktop.portal.ScreenCast"
)

// SourceType is a bitmask indicating what types of content to record.
type SourceType uint32

const (
	SourceMonitor SourceType = 1 << iota // 1
	SourceWindow                         // 2
	SourceVirtual                        // 4
)

// CursorMode determines how the cursor is rendered in the stream.
type CursorMode uint32

const (
	CursorHidden   CursorMode = 1 << iota // 1
	CursorEmbedded                        // 2
	CursorMetadata                        // 4
)

// StreamInfo holds metadata about a single PipeWire screen cast stream
// returned by the portal after the user selects sources.
type StreamInfo struct {
	NodeID     uint32     // PipeWire node ID
	Position   [2]int32   // (x, y) in compositor space; zero if unavailable
	Size       [2]int32   // (width, height) in compositor space; zero if unavailable
	SourceType SourceType // What kind of source this stream represents
}

// ScreenCastSession represents an active XDG Desktop Portal screen cast session.
// Create one via Client.CreateScreenCastSession.
type ScreenCastSession struct {
	client        *Client
	sessionHandle dbus.ObjectPath

	// Streams is populated after Start() succeeds.
	Streams []StreamInfo

	// PipeWireFD is populated after OpenPipeWireRemote() succeeds.
	// The caller is responsible for closing this FD when done.
	PipeWireFD int
}

// SessionHandle returns the D-Bus object path for this session.
func (s *ScreenCastSession) SessionHandle() dbus.ObjectPath {
	return s.sessionHandle
}

// CreateScreenCastSession creates a new ScreenCast session via the portal.
// The session must be closed by the caller when it is no longer needed.
func (c *Client) CreateScreenCastSession(ctx context.Context) (*ScreenCastSession, error) {
	c.logger.Info("creating screencast session")

	// Generate a unique session handle token.
	seq := c.requestCounter.Add(1)
	sessionToken := fmt.Sprintf("waydesk_session_%d", seq)

	options := map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant(sessionToken),
	}

	resp, err := c.call(ctx, screenCastIface+".CreateSession", options)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Extract session_handle from the response.
	handleVariant, ok := resp.Results["session_handle"]
	if !ok {
		return nil, fmt.Errorf("create session: response missing session_handle")
	}
	handleStr, ok := handleVariant.Value().(string)
	if !ok {
		return nil, fmt.Errorf("create session: session_handle is not a string")
	}

	session := &ScreenCastSession{
		client:        c,
		sessionHandle: dbus.ObjectPath(handleStr),
		PipeWireFD:    -1,
	}

	c.logger.Info("screencast session created",
		"session_handle", session.sessionHandle,
	)

	return session, nil
}

// SelectSources configures what the session should record. This must be
// called exactly once before Start(). It triggers the compositor's source
// selection dialog.
func (s *ScreenCastSession) SelectSources(ctx context.Context, types SourceType, cursor CursorMode) error {
	s.client.logger.Info("selecting sources",
		"types", types,
		"cursor_mode", cursor,
	)

	options := map[string]dbus.Variant{
		"types":       dbus.MakeVariant(uint32(types)),
		"multiple":    dbus.MakeVariant(false),
		"cursor_mode": dbus.MakeVariant(uint32(cursor)),
	}

	_, err := s.client.call(
		ctx,
		screenCastIface+".SelectSources",
		options,
		s.sessionHandle,
	)
	if err != nil {
		return fmt.Errorf("select sources: %w", err)
	}

	s.client.logger.Info("sources selected")
	return nil
}

// Start begins the screen cast. After a successful call, s.Streams will
// contain info about each PipeWire stream node.
func (s *ScreenCastSession) Start(ctx context.Context) error {
	s.client.logger.Info("starting screencast")

	options := map[string]dbus.Variant{}

	resp, err := s.client.call(
		ctx,
		screenCastIface+".Start",
		options,
		s.sessionHandle,
		"", // parent_window — empty for headless / CLI
	)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// Parse the streams from the response.
	// The portal returns: streams a(ua{sv})
	streamsVariant, ok := resp.Results["streams"]
	if !ok {
		return fmt.Errorf("start: response missing streams")
	}

	streams, err := parseStreams(streamsVariant)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}

	s.Streams = streams

	for i, st := range s.Streams {
		s.client.logger.Info("stream discovered",
			"index", i,
			"node_id", st.NodeID,
			"size", st.Size,
			"position", st.Position,
			"source_type", st.SourceType,
		)
	}

	return nil
}

// OpenPipeWireRemote requests a file descriptor for the PipeWire remote
// associated with this session. The FD can be used with
// pw_context_connect_fd() to receive video frames.
func (s *ScreenCastSession) OpenPipeWireRemote(ctx context.Context) error {
	s.client.logger.Info("opening pipewire remote")

	options := map[string]dbus.Variant{}

	call := s.client.conn.Object(portalBusName, portalObjectPath).CallWithContext(
		ctx,
		screenCastIface+".OpenPipeWireRemote",
		0,
		s.sessionHandle,
		options,
	)
	if call.Err != nil {
		return fmt.Errorf("open pipewire remote: %w", call.Err)
	}

	// The FD is passed out-of-band via SCM_RIGHTS. godbus represents this
	// as a dbus.UnixFDIndex in the message body. We retrieve the actual
	// FD from the call's file descriptor list.
	if len(call.Body) < 1 {
		return fmt.Errorf("open pipewire remote: empty response body")
	}

	switch v := call.Body[0].(type) {
	case dbus.UnixFDIndex:
		s.PipeWireFD = int(v)
	case dbus.UnixFD:
		s.PipeWireFD = int(v)
	case int32:
		s.PipeWireFD = int(v)
	default:
		return fmt.Errorf("open pipewire remote: unexpected FD type %T", call.Body[0])
	}

	s.client.logger.Info("pipewire remote opened", "fd", s.PipeWireFD)
	return nil
}

// Close terminates the portal session, releasing all associated resources.
func (s *ScreenCastSession) Close() error {
	s.client.logger.Info("closing screencast session", "session_handle", s.sessionHandle)

	call := s.client.conn.Object(
		portalBusName,
		s.sessionHandle,
	).Call(sessionIface+".Close", 0)

	if call.Err != nil {
		return fmt.Errorf("close session: %w", call.Err)
	}

	s.client.logger.Info("screencast session closed")
	return nil
}

// parseStreams extracts StreamInfo entries from the portal's streams variant.
// The wire format is: a(ua{sv}) — an array of tuples (nodeID, properties).
func parseStreams(v dbus.Variant) ([]StreamInfo, error) {
	// godbus decodes a(ua{sv}) as [][]interface{}, where each inner slice
	// has two elements: uint32 (node ID) and map[string]dbus.Variant (props).
	raw, ok := v.Value().([][]interface{})
	if !ok {
		return nil, fmt.Errorf("parse streams: unexpected type %T", v.Value())
	}

	streams := make([]StreamInfo, 0, len(raw))
	for _, entry := range raw {
		if len(entry) < 2 {
			return nil, fmt.Errorf("parse streams: stream entry has %d elements, expected 2", len(entry))
		}

		nodeID, ok := entry[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("parse streams: node ID is not uint32: %T", entry[0])
		}

		props, ok := entry[1].(map[string]dbus.Variant)
		if !ok {
			return nil, fmt.Errorf("parse streams: properties is not map[string]Variant: %T", entry[1])
		}

		info := StreamInfo{NodeID: nodeID}

		// Extract optional size.
		if sizeV, ok := props["size"]; ok {
			if size, ok := sizeV.Value().([2]int32); ok {
				info.Size = size
			}
			// Some compositors return (ii) as a struct — try []interface{}.
			if sizeSlice, ok := sizeV.Value().([]interface{}); ok && len(sizeSlice) == 2 {
				if w, ok := sizeSlice[0].(int32); ok {
					info.Size[0] = w
				}
				if h, ok := sizeSlice[1].(int32); ok {
					info.Size[1] = h
				}
			}
		}

		// Extract optional position.
		if posV, ok := props["position"]; ok {
			if pos, ok := posV.Value().([2]int32); ok {
				info.Position = pos
			}
			if posSlice, ok := posV.Value().([]interface{}); ok && len(posSlice) == 2 {
				if x, ok := posSlice[0].(int32); ok {
					info.Position[0] = x
				}
				if y, ok := posSlice[1].(int32); ok {
					info.Position[1] = y
				}
			}
		}

		// Extract optional source_type.
		if stV, ok := props["source_type"]; ok {
			if st, ok := stV.Value().(uint32); ok {
				info.SourceType = SourceType(st)
			}
		}

		streams = append(streams, info)
	}

	return streams, nil
}

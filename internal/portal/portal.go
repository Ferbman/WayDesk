// Package portal provides a production-grade D-Bus client for interacting with
// the XDG Desktop Portal. It handles session bus connections, request/response
// signal management, and the unique handle_token protocol required by the portal.
package portal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
)

const (
	portalBusName    = "org.freedesktop.portal.Desktop"
	portalObjectPath = "/org/freedesktop/portal/desktop"

	requestIface = "org.freedesktop.portal.Request"
	sessionIface = "org.freedesktop.portal.Session"
)

// ResponseCode represents the result of a portal Request.
type ResponseCode uint32

const (
	ResponseSuccess   ResponseCode = 0
	ResponseCancelled ResponseCode = 1
	ResponseOther     ResponseCode = 2
)

// Client manages the D-Bus connection to the XDG Desktop Portal.
// It is safe to create multiple sessions from a single Client,
// but the Client itself is NOT safe for concurrent use.
type Client struct {
	conn   *dbus.Conn
	logger *slog.Logger

	// senderName is the unique bus name (e.g. ":1.42") with dots and colons
	// replaced by underscores, as required by the portal handle protocol.
	senderName string

	// requestCounter generates unique handle tokens per-client.
	requestCounter atomic.Uint64
}

// NewClient connects to the session D-Bus and prepares a portal client.
// The provided context is used only for the initial connection; subsequent
// calls use their own contexts.
func NewClient(ctx context.Context, logger *slog.Logger) (*Client, error) {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("portal: connect session bus: %w", err)
	}

	// The sender name looks like ":1.42". The portal requires it in object
	// path form with dots/colons replaced by underscores: "_1_42".
	// Wait, the portal actually formats it as "1_42" (stripping the leading colon).
	raw := conn.Names()[0]
	sanitized := strings.TrimPrefix(raw, ":")
	sanitized = strings.ReplaceAll(sanitized, ".", "_")

	logger.Info("portal client created", "sender", raw, "sanitized_sender", sanitized)

	return &Client{
		conn:       conn,
		logger:     logger,
		senderName: sanitized,
	}, nil
}

// Close shuts down the D-Bus connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// portalObject returns the proxy for the main portal object.
func (c *Client) portalObject() dbus.BusObject {
	return c.conn.Object(portalBusName, portalObjectPath)
}

// callResponse holds the parsed result of a portal Request::Response signal.
type callResponse struct {
	Code    ResponseCode
	Results map[string]dbus.Variant
}

// call makes an asynchronous portal D-Bus call. It:
//  1. Generates a unique handle_token
//  2. Subscribes to the Response signal on the predicted Request path BEFORE
//     making the call (this ordering is critical — the portal may respond
//     before the call returns).
//  3. Invokes the D-Bus method with the provided args.
//  4. Waits for the Response signal or context cancellation.
//
// The caller must include "handle_token" in their options map; this method
// will set it to the generated token.
func (c *Client) call(
	ctx context.Context,
	method string,
	options map[string]dbus.Variant,
	args ...interface{},
) (*callResponse, error) {
	// Generate a unique token for this request.
	seq := c.requestCounter.Add(1)
	token := fmt.Sprintf("waydesk_%d", seq)
	options["handle_token"] = dbus.MakeVariant(token)

	// Predict the Request object path. The portal documentation specifies:
	//   /org/freedesktop/portal/desktop/request/{sender}/{token}
	expectedPath := dbus.ObjectPath(
		fmt.Sprintf("/org/freedesktop/portal/desktop/request/%s/%s", c.senderName, token),
	)

	// Subscribe to the Response signal BEFORE making the call.
	// This prevents a race where the portal sends the signal before we listen.
	sigCh := make(chan *dbus.Signal, 1)
	c.conn.Signal(sigCh)

	matchRule := fmt.Sprintf(
		"type='signal',sender='%s',interface='%s',member='Response',path='%s'",
		portalBusName, requestIface, expectedPath,
	)
	if err := c.conn.BusObject().CallWithContext(
		ctx, "org.freedesktop.DBus.AddMatch", 0, matchRule,
	).Err; err != nil {
		c.conn.RemoveSignal(sigCh)
		return nil, fmt.Errorf("portal: add match rule: %w", err)
	}

	// Ensure cleanup of signal subscription.
	defer func() {
		c.conn.RemoveSignal(sigCh)
		// Best-effort removal of match rule; ignore errors.
		_ = c.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, matchRule).Err
	}()

	// Prepare full argument list: prepend any positional args, append options.
	fullArgs := append(args, options)

	c.logger.Debug("portal call",
		"method", method,
		"token", token,
		"expected_path", expectedPath,
	)

	// Make the D-Bus call.
	call := c.portalObject().CallWithContext(ctx, method, 0, fullArgs...)
	if call.Err != nil {
		return nil, fmt.Errorf("portal: call %s: %w", method, call.Err)
	}

	// Wait for the Response signal.
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("portal: waiting for response to %s: %w", method, ctx.Err())
		case sig := <-sigCh:
			if sig.Path != expectedPath || sig.Name != requestIface+".Response" {
				continue // Not our signal; skip.
			}

			if len(sig.Body) < 2 {
				return nil, fmt.Errorf("portal: malformed Response signal for %s: expected 2 body elements, got %d", method, len(sig.Body))
			}

			code, ok := sig.Body[0].(uint32)
			if !ok {
				return nil, fmt.Errorf("portal: Response code is not uint32 for %s", method)
			}

			results, ok := sig.Body[1].(map[string]dbus.Variant)
			if !ok {
				return nil, fmt.Errorf("portal: Response results is not map[string]Variant for %s", method)
			}

			resp := &callResponse{
				Code:    ResponseCode(code),
				Results: results,
			}

			if resp.Code != ResponseSuccess {
				return resp, fmt.Errorf("portal: %s returned non-success response: %d", method, resp.Code)
			}

			c.logger.Debug("portal response",
				"method", method,
				"code", resp.Code,
			)

			return resp, nil
		}
	}
}

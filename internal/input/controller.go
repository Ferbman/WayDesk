package input

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/bendahl/uinput"
)

// Controller manages the virtual uinput devices to simulate keyboard and mouse
type Controller struct {
	keyboard uinput.Keyboard
	mouse    uinput.Mouse // Relative movements (Trackpad mode fallback/scrolls)
	touchpad uinput.TouchPad // Absolute targeting (Click Teleportation)
	logger   *slog.Logger

	totalWidth  int32
	totalHeight int32
}

// fetchHyprlandDesktopSize executes `hyprctl` to find the global X,Y bounding box of all monitors.
// This is critical to prevent `uinput` from stretching coords across multiple screens incorrectly!
func fetchHyprlandDesktopSize() (int32, int32, error) {
	out, err := exec.Command("hyprctl", "monitors", "-j").Output()
	if err != nil {
		return 0, 0, err
	}

	type Monitor struct {
		X      int32 `json:"x"`
		Y      int32 `json:"y"`
		Width  int32 `json:"width"`
		Height int32 `json:"height"`
	}

	var monitors []Monitor
	if err := json.Unmarshal(out, &monitors); err != nil {
		return 0, 0, err
	}

	var maxX, maxY int32
	for _, m := range monitors {
		if m.X+m.Width > maxX {
			maxX = m.X + m.Width
		}
		if m.Y+m.Height > maxY {
			maxY = m.Y + m.Height
		}
	}

	// Fallback minimum bounds
	if maxX == 0 || maxY == 0 {
		return 1920, 1080, nil
	}

	return maxX, maxY, nil
}

// NewController creates virtual input devices mapped to the specified screen resolution.
func NewController(logger *slog.Logger) (*Controller, error) {
	kb, err := uinput.CreateKeyboard("/dev/uinput", []byte("WayDesk Virtual Keyboard"))
	if err != nil {
		return nil, fmt.Errorf("create keyboard: %w", err)
	}

	// Relative Mouse
	m, err := uinput.CreateMouse("/dev/uinput", []byte("WayDesk Virtual Mouse"))
	if err != nil {
		kb.Close()
		return nil, fmt.Errorf("create mouse: %w", err)
	}

	// Global Total Resolution Touchpad
	totW, totH, err := fetchHyprlandDesktopSize()
	if err != nil {
		totW, totH = 1920, 1080 // Fallback
		logger.Warn("could not fetch hyprctl bounds, falling back to 1080p for absolute teleporting", "err", err)
	}

	tp, err := uinput.CreateTouchPad("/dev/uinput", []byte("WayDesk Absolute Teleporter"), 0, totW, 0, totH)
	if err != nil {
		kb.Close()
		m.Close()
		return nil, fmt.Errorf("create touchpad: %w", err)
	}
	
	logger.Info("init virtual teleporter touchpad bounds", "width", totW, "height", totH)

	return &Controller{
		keyboard:    kb,
		mouse:       m,
		touchpad:    tp,
		logger:      logger,
		totalWidth:  totW,
		totalHeight: totH,
	}, nil
}

// TeleportClick maps the normalized coordinates (cx, cy) from the shared stream's resolution,
// adds the stream's position offset (offsetX, offsetY), and sends an absolute touch exactly
// to that global coordinate over the entire desktop.
func (c *Controller) TeleportClick(cx, cy float64, streamOffsetX, streamOffsetY, streamWidth, streamHeight int32, button int, down bool) error {
	// First convert normalized to stream pixel
	localX := int32(cx * float64(streamWidth))
	localY := int32(cy * float64(streamHeight))
	
	// Add offset to reach global position
	globalX := streamOffsetX + localX
	globalY := streamOffsetY + localY
	
	// Ensure we don't breach bounds logically
	if globalX > c.totalWidth { globalX = c.totalWidth }
	if globalY > c.totalHeight { globalY = c.totalHeight }
	
	c.logger.Debug("absolute click teleporter injected", "localX", localX, "localY", localY, "globalX", globalX, "globalY", globalY)

	// Move cursor
	if err := c.touchpad.MoveTo(globalX, globalY); err != nil {
		return err
	}

	// Execute Click
	switch button {
	case 0:
		if down {
			return c.touchpad.LeftPress()
		}
		return c.touchpad.LeftRelease()
	case 1:
		if down {
			return c.mouse.MiddlePress() // mouse is relative but press holds shared state
		}
		return c.mouse.MiddleRelease()
	case 2:
		if down {
			return c.touchpad.RightPress()
		}
		return c.touchpad.RightRelease()
	default:
		c.logger.Debug("unsupported click teleporter button", "button", button)
		return nil
	}
}

// MoveRelative injects a relative mouse movement (dx, dy mapped to trackpad scaling)
func (c *Controller) MoveRelative(dx, dy float64) error {
	// The client is now sending raw pixel differences (e.g. trailing e.clientX).
	// We can add a slight acceleration / sensitivity factor so phone movements
	// easily traverse a 2K desktop monitor.
	x := int32(dx * 1.5)
	y := int32(dy * 1.5)
	return c.mouse.Move(x, y)
}

// MouseButton translates a web button index (0=Left, 1=Middle, 2=Right) into a uinput action
func (c *Controller) MouseButton(button int, down bool) error {
	switch button {
	case 0:
		if down {
			return c.mouse.LeftPress()
		}
		return c.mouse.LeftRelease()
	case 1:
		if down {
			return c.mouse.MiddlePress()
		}
		return c.mouse.MiddleRelease()
	case 2:
		if down {
			return c.mouse.RightPress()
		}
		return c.mouse.RightRelease()
	default:
		c.logger.Debug("unsupported mouse button", "button", button)
		return nil
	}
}

func (c *Controller) MouseWheel(deltaY int32) error {
	return c.mouse.Wheel(false, deltaY)
}

// KeyPress sends a down/up event for a linux keycode. We map web event.code to uinput keycodes.
func (c *Controller) KeyPress(linuxKeyCode int, down bool) error {
	if linuxKeyCode == 0 {
		return nil
	}
	if down {
		return c.keyboard.KeyDown(linuxKeyCode)
	}
	return c.keyboard.KeyUp(linuxKeyCode)
}

// Close destroys the virtual inputs
func (c *Controller) Close() error {
	c.logger.Info("closing virtual input devices")
	var err1, err2 error
	if c.keyboard != nil {
		err1 = c.keyboard.Close()
	}
	if c.mouse != nil {
		err2 = c.mouse.Close()
	}
	if err1 != nil {
		return err1
	}
	return err2
}

package input

import (
	"fmt"
	"log/slog"

	"github.com/bendahl/uinput"
)

// Controller manages the virtual uinput devices to simulate keyboard and mouse
type Controller struct {
	keyboard uinput.Keyboard
	mouse    uinput.Mouse
	logger   *slog.Logger
}

// NewController creates virtual input devices mapped to the specified screen resolution.
func NewController(logger *slog.Logger) (*Controller, error) {
	kb, err := uinput.CreateKeyboard("/dev/uinput", []byte("WayDesk Virtual Keyboard"))
	if err != nil {
		return nil, fmt.Errorf("create keyboard: %w", err)
	}

	// Utilizing a relative Mouse instead of an Absolute Touchpad.
	// Absolute touches are stretched by Wayland across ALL monitors in a multi-monitor setup.
	// Relative movements (dx, dy) bypass this completely and move the cursor wherever it already is,
	// acting just like a physical hardware mouse.
	m, err := uinput.CreateMouse("/dev/uinput", []byte("WayDesk Virtual Mouse"))
	if err != nil {
		kb.Close()
		return nil, fmt.Errorf("create mouse: %w", err)
	}

	return &Controller{
		keyboard: kb,
		mouse:    m,
		logger:   logger,
	}, nil
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

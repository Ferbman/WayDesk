package ui

import (
	"log/slog"
	"math"
	"sync"
	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/dlasky/gotk3-layershell/layershell"
)

// CursorState holds the thread-safe coordinates of the fake cursor overlay.
type CursorState struct {
	sync.RWMutex
	X, Y    float64
	Visible bool
	Name    string
}

// Global state for simplicity
var State = &CursorState{Name: "Guest", Visible: false}
var mainWin *gtk.Window
var drawingArea *gtk.DrawingArea

// UpdatePosition updates the internal coordinates.
// Instead of drawing on a full-screen invisible canvas,
// we physically move a tiny GTK window across the screen using Layer Shell margins!
func UpdatePosition(x, y float64, visible bool) {
	State.Lock()
	State.X = x
	State.Y = y
	State.Visible = visible
	State.Unlock()

	glib.IdleAdd(func() bool {
		if mainWin != nil {
			posX := int(math.Round(x))
			posY := int(math.Round(y))

			layershell.SetMargin(mainWin, layershell.LAYER_SHELL_EDGE_LEFT, posX)
			layershell.SetMargin(mainWin, layershell.LAYER_SHELL_EDGE_TOP, posY)

			if visible {
				mainWin.ShowAll()
			} else {
				mainWin.Hide()
			}
			
			if drawingArea != nil {
				drawingArea.QueueDraw()
			}
		}
		return false
	})
}

// StartOverlay initializes GTK and runs the main loop.
// THIS MUST BE CALLED FROM THE MAIN GOROUTINE!
func StartOverlay(logger *slog.Logger) {
	gtk.Init(nil)
	logger.Info("layer shell transparent overlay initialized, waiting for stream...")
	gtk.Main()
}

// CreateWindow generates the layered micro-window strictly pinned to the provided XDG coordinates.
// It iterates GDK displays to match the exact stream position, supporting multi-monitor selection.
func CreateWindow(offsetX, offsetY int, logger *slog.Logger) {
	glib.IdleAdd(func() bool {
		win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
		if err != nil {
			logger.Error("failed to create fake cursor window", "err", err)
			return false
		}
		mainWin = win

		win.SetSizeRequest(120, 48)

		layershell.InitForWindow(win)
		layershell.SetNamespace(win, "waydesk-cursor")
		layershell.SetLayer(win, layershell.LAYER_SHELL_LAYER_OVERLAY)

		// Anchor ONLY to Top-Left so we can use margins as X,Y coords
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_TOP, true)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_LEFT, true)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_RIGHT, false)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_BOTTOM, false)

		// Discover the correct GDK monitor matching the stream offset
		display, err := gdk.DisplayGetDefault()
		if err == nil && display != nil {
			n := display.GetNMonitors()
			for i := 0; i < n; i++ {
				mon, _ := display.GetMonitor(i)
				if mon != nil {
					geom := mon.GetGeometry()
					if geom.GetX() == offsetX && geom.GetY() == offsetY {
						layershell.SetMonitor(win, mon)
						logger.Info("bound GTK overlay to specific monitor", "index", i, "x", offsetX, "y", offsetY)
						break
					}
				}
			}
		}

		layershell.SetExclusiveZone(win, -1)
		layershell.SetKeyboardMode(win, layershell.LAYER_SHELL_KEYBOARD_MODE_NONE)

		screen := win.GetScreen()
		visual, _ := screen.GetRGBAVisual()
		if visual != nil && screen.IsComposited() {
			win.SetVisual(visual)
		}

		drawingArea, err = gtk.DrawingAreaNew()
		if err != nil {
			logger.Error("failed to create drawing area", "err", err)
			return false
		}

		drawingArea.Connect("draw", func(_ *gtk.DrawingArea, ctx *cairo.Context) {
			ctx.SetSourceRGBA(0, 0, 0, 0)
			ctx.SetOperator(cairo.OPERATOR_SOURCE)
			ctx.Paint()
			ctx.SetOperator(cairo.OPERATOR_OVER)

			State.RLock()
			name := State.Name
			State.RUnlock()

			cx, cy := 0.0, 0.0

			ctx.MoveTo(cx, cy)
			ctx.LineTo(cx+16, cy+16)
			ctx.LineTo(cx+10, cy+20)
			ctx.LineTo(cx, cy+28)
			ctx.ClosePath()
			ctx.SetSourceRGBA(1.0, 0.2, 0.2, 1.0)
			ctx.FillPreserve()

			ctx.SetSourceRGBA(1.0, 1.0, 1.0, 1.0)
			ctx.SetLineWidth(1.5)
			ctx.Stroke()

			ctx.MoveTo(cx+18, cy+20)
			ctx.SetFontSize(16.0)
			ctx.SetSourceRGBA(0, 0, 0, 0.8)
			ctx.ShowText(name)

			ctx.MoveTo(cx+17, cy+19)
			ctx.SetSourceRGBA(1.0, 1.0, 1.0, 1.0)
			ctx.ShowText(name)
		})

		win.Add(drawingArea)
		win.Hide()
		
		return false
	})
}

// StopOverlay stops the GTK main loop.
func StopOverlay() {
	glib.IdleAdd(func() bool {
		gtk.MainQuit()
		return false
	})
}

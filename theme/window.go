package theme

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	mylayout "honnef.co/go/gotraceui/layout"
)

type Window struct {
	Theme *Theme
	Menu  *Menu

	pointerAt f32.Point
	modal     Modal

	contextMenu struct {
		at f32.Point
		w  Widget
	}
	windowFrameState
}

type windowFrameState struct {
	tooltip Widget
}

type Widget func(win *Window, gtx layout.Context) layout.Dimensions

func Dumb(win *Window, w Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return w(win, gtx)
	}
}

func (win *Window) Render(ops *op.Ops, ev system.FrameEvent, w func(win *Window, gtx layout.Context) layout.Dimensions) {
	gtx := layout.NewContext(ops, ev)

	win.windowFrameState = windowFrameState{}

	for _, ev := range gtx.Events(win) {
		switch ev := ev.(type) {
		case pointer.Event:
			win.pointerAt = ev.Position
		}
	}

	stack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pointer.InputOp{Tag: win, Types: 0xFF}.Add(gtx.Ops)

	if win.Menu != nil {
		dims := win.Menu.Layout(win, gtx)
		mylayout.PixelInset{
			Top: dims.Size.Y,
		}.Layout(gtx, Dumb(win, w))
	} else {
		w(win, gtx)
	}
	stack.Pop()

	if win.tooltip != nil {
		// TODO have a gap between the cursor and the tooltip
		macro := op.Record(gtx.Ops)
		dims := win.tooltip(win, gtx)
		call := macro.Stop()

		var x, y int
		ptr := win.pointerAt.Round()
		if ptr.X+dims.Size.X < gtx.Constraints.Max.X {
			x = ptr.X
		} else {
			x = gtx.Constraints.Max.X - dims.Size.X
		}
		if ptr.Y+dims.Size.Y < gtx.Constraints.Max.Y {
			y = ptr.Y
		} else {
			y = gtx.Constraints.Max.Y - dims.Size.Y
		}

		stack := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		stack.Pop()
	}

	if win.modal.Cancelled() {
		win.contextMenu.w = nil
	}

	if win.contextMenu.w != nil {
		gtx := gtx
		gtx.Constraints.Min = image.Point{}
		// FIXME(dh): don't render context menu out of bounds
		win.modal.Layout(win, gtx, func(win *Window, gtx layout.Context) layout.Dimensions {
			defer op.Offset(win.contextMenu.at.Round()).Push(gtx.Ops).Pop()
			return win.contextMenu.w(win, gtx)
		})
	}
}

func (win *Window) SetTooltip(w Widget) {
	win.tooltip = w
}

func (win *Window) SetContextMenu(w Widget) {
	win.contextMenu.at = win.pointerAt
	win.contextMenu.w = w
}

func (win *Window) CloseContextMenu() {
	win.contextMenu.w = nil
}

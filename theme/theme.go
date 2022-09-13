package theme

import (
	"image"
	"image/color"

	mywidget "honnef.co/go/gotraceui/widget"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

type Theme struct {
	Shaper        text.Shaper
	Palette       Palette
	TextSize      unit.Sp
	TextSizeLarge unit.Sp

	WindowPadding unit.Dp
	WindowBorder  unit.Dp
}

type Palette struct {
	Background color.NRGBA
	Foreground color.NRGBA
	Link       color.NRGBA

	WindowBorder     color.NRGBA
	WindowBackground color.NRGBA
}

var DefaultPalette = Palette{
	Background: rgba(0xFFFFEAFF),
	Foreground: rgba(0x000000FF),
	Link:       rgba(0x0000FFFF),

	WindowBorder:     rgba(0x000000FF),
	WindowBackground: rgba(0xEEFFEEFF),
}

func NewTheme(fontCollection []text.FontFace) *Theme {
	return &Theme{
		Palette:       DefaultPalette,
		Shaper:        text.NewCache(fontCollection),
		TextSize:      12,
		TextSizeLarge: 14,

		WindowPadding: 2,
		WindowBorder:  1,
	}
}

type ProgressBarStyle struct {
	ForegroundColor color.NRGBA
	BackgroundColor color.NRGBA
	BorderWidth     unit.Dp
	Progress        float32
}

func ProgressBar(th *Theme, progress float32) ProgressBarStyle {
	return ProgressBarStyle{
		ForegroundColor: rgba(0x478847FF),
		BackgroundColor: rgba(0),
		BorderWidth:     1,
		Progress:        progress,
	}
}

func (p ProgressBarStyle) Layout(gtx layout.Context) layout.Dimensions {
	return mywidget.Border{
		Color: p.ForegroundColor,
		Width: p.BorderWidth,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Draw background
		bg := clip.Rect{Max: gtx.Constraints.Min}.Op()
		paint.FillShape(gtx.Ops, p.BackgroundColor, bg)

		// Draw foreground
		fg := frect{Max: f32.Pt(float32(gtx.Constraints.Min.X)*p.Progress, float32(gtx.Constraints.Min.Y))}.Op(gtx.Ops)
		paint.FillShape(gtx.Ops, p.ForegroundColor, fg)

		return layout.Dimensions{
			Size: gtx.Constraints.Min,
		}
	})
}

type CheckBoxStyle struct {
	Checkbox        *widget.Bool
	Label           string
	TextSize        unit.Sp
	ForegroundColor color.NRGBA
	BackgroundColor color.NRGBA
	TextColor       color.NRGBA

	shaper text.Shaper
}

func CheckBox(th *Theme, checkbox *widget.Bool, label string) CheckBoxStyle {
	return CheckBoxStyle{
		Checkbox:        checkbox,
		Label:           label,
		TextColor:       rgba(0x000000FF),
		ForegroundColor: rgba(0x000000FF),
		BackgroundColor: rgba(0),
		TextSize:        12,

		shaper: th.Shaper,
	}
}

func (c CheckBoxStyle) Layout(gtx layout.Context) layout.Dimensions {
	return c.Checkbox.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sizeDp := gtx.Metric.SpToDp(c.TextSize)
				sizePx := gtx.Dp(sizeDp)

				ngtx := gtx
				ngtx.Constraints = layout.Exact(image.Pt(sizePx, sizePx))
				return mywidget.Border{
					Color: c.ForegroundColor,
					Width: 1,
				}.Layout(ngtx, func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, c.BackgroundColor, clip.Rect{Max: gtx.Constraints.Min}.Op())
					if c.Checkbox.Value {
						padding := gtx.Constraints.Min.X / 4
						if padding == 0 {
							padding = gtx.Dp(1)
						}
						minx := padding
						miny := minx
						maxx := gtx.Constraints.Min.X - padding
						maxy := maxx
						paint.FillShape(gtx.Ops, c.ForegroundColor, clip.Rect{Min: image.Pt(minx, miny), Max: image.Pt(maxx, maxy)}.Op())
					}

					return layout.Dimensions{Size: gtx.Constraints.Min}
				})
			}),

			layout.Rigid(layout.Spacer{Width: 3}.Layout),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return mywidget.TextLine{Color: c.TextColor}.Layout(gtx, c.shaper, text.Font{}, c.TextSize, c.Label)
			}),
		)
	})
}

func rgba(c uint32) color.NRGBA {
	// XXX does endianness matter?
	return color.NRGBA{
		A: uint8(c & 0xFF),
		B: uint8(c >> 8 & 0xFF),
		G: uint8(c >> 16 & 0xFF),
		R: uint8(c >> 24 & 0xFF),
	}
}

type frect struct {
	Min f32.Point
	Max f32.Point
}

func (r frect) Path(ops *op.Ops) clip.PathSpec {
	var p clip.Path
	p.Begin(ops)
	r.IntoPath(&p)
	return p.End()
}

func (r frect) IntoPath(p *clip.Path) {
	p.MoveTo(r.Min)
	p.LineTo(f32.Pt(r.Max.X, r.Min.Y))
	p.LineTo(r.Max)
	p.LineTo(f32.Pt(r.Min.X, r.Max.Y))
	p.LineTo(r.Min)
}

func (r frect) Op(ops *op.Ops) clip.Op {
	return clip.Outline{Path: r.Path(ops)}.Op()
}

func max(a, b int) int {
	if a >= b {
		return a
	} else {
		return b
	}
}

// clamp1 limits v to range [0..1].
func clamp1(v float32) float32 {
	if v >= 1 {
		return 1
	} else if v <= 0 {
		return 0
	} else {
		return v
	}
}

// mulAlpha applies the alpha to the color.
func mulAlpha(c color.NRGBA, alpha uint8) color.NRGBA {
	c.A = uint8(uint32(c.A) * uint32(alpha) / 0xFF)
	return c
}

type Foldable struct {
	// TODO(dh): move state into widget package

	Theme  *Theme
	Title  string
	Closed widget.Bool
}

func (f *Foldable) Layout(gtx layout.Context, contents layout.Widget) layout.Dimensions {
	var size image.Point
	dims := f.Closed.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// TODO(dh): show an icon indicating state of the foldable. We tried using ▶ and ▼ but the Go font only has ▼…
		var l string
		if f.Closed.Value {
			l = "[C] " + f.Title
		} else {
			l = "[O] " + f.Title
		}
		gtx.Constraints.Min.Y = 0
		paint.ColorOp{Color: f.Theme.Palette.Foreground}.Add(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		return widget.Label{MaxLines: 1}.Layout(gtx, f.Theme.Shaper, text.Font{Weight: text.Bold}, f.Theme.TextSize, l)

	})
	size = dims.Size

	if !f.Closed.Value {
		defer op.Offset(image.Pt(0, size.Y)).Push(gtx.Ops).Pop()
		gtx.Constraints.Max.Y -= size.Y
		dims := contents(gtx)

		max := func(a, b int) int {
			if a >= b {
				return a
			} else {
				return b
			}
		}
		size.X = max(size.X, dims.Size.X)
		size.Y += dims.Size.Y
	}

	size = gtx.Constraints.Constrain(size)
	return layout.Dimensions{Size: size}
}

type Tooltip struct {
	Theme *Theme
}

func (tt Tooltip) Layout(gtx layout.Context, l string) layout.Dimensions {
	return BorderedText(gtx, tt.Theme, l)
}

func BorderedText(gtx layout.Context, th *Theme, s string) layout.Dimensions {
	return mywidget.Bordered{Color: th.Palette.WindowBorder, Width: th.WindowBorder}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
		// Don't inherit the minimum constraint from the parent widget. In this specific case, this widget is being
		// rendered as part of a flex child.
		gtx.Constraints.Min = image.Pt(0, 0)
		var padding = gtx.Dp(th.WindowPadding)

		macro := op.Record(gtx.Ops)
		paint.ColorOp{Color: th.Palette.Foreground}.Add(gtx.Ops)
		dims := widget.Label{}.Layout(gtx, th.Shaper, text.Font{}, th.TextSize, s)
		call := macro.Stop()

		total := clip.Rect{
			Min: image.Pt(0, 0),
			Max: image.Pt(dims.Size.X+2*padding, dims.Size.Y+2*padding),
		}

		paint.FillShape(gtx.Ops, th.Palette.WindowBackground, total.Op())

		stack := op.Offset(image.Pt(padding, padding)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		stack.Pop()

		return layout.Dimensions{
			Baseline: dims.Baseline,
			Size:     total.Max,
		}
	})
}

type ButtonStyle struct {
	Text   string
	Button *widget.Clickable
	shaper text.Shaper
}

func Button(th *Theme, button *widget.Clickable, txt string) ButtonStyle {
	return ButtonStyle{
		Text:   txt,
		Button: button,
		shaper: th.Shaper,
	}
}

func (b ButtonStyle) Layout(gtx layout.Context) layout.Dimensions {
	return b.Button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return mywidget.Bordered{Color: rgba(0x000000FF), Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{Alignment: layout.Center}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					if b.Button.Pressed() {
						paint.FillShape(gtx.Ops, rgba(0xFFFF00FF), clip.Rect{Max: gtx.Constraints.Min}.Op())
					} else {
						paint.FillShape(gtx.Ops, rgba(0xFFFFFFFF), clip.Rect{Max: gtx.Constraints.Min}.Op())
					}
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					paint.ColorOp{Color: rgba(0x000000FF)}.Add(gtx.Ops)
					return widget.Label{Alignment: text.Middle}.Layout(gtx, b.shaper, text.Font{}, 12, b.Text)
				}),
			)
		})
	})
}

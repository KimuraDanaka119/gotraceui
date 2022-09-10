package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"time"

	"honnef.co/go/gotraceui/theme"
	mywidget "honnef.co/go/gotraceui/widget"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"
)

type heatmapCacheKey struct {
	size            image.Point
	useLinearColors bool
	yBucketSize     int
}

type Heatmap struct {
	UseLinearColors bool
	YBucketSize     int

	xBucketSize time.Duration
	maxY        int
	numXBuckets int
	numYBuckets int
	data        []int

	// We store the original data as this allows us to change the yStep and recompute the buckets.
	origData [][]int

	pointer f32.Point
	// pointerConstraint records the constraint when we captured the pointer position. This is to avoid using outdated
	// positions when the window size changes without causing new pointer move events.
	pointerConstraint image.Point

	hovered HeatmapBucket

	cacheKey    heatmapCacheKey
	cachedOps   op.Ops
	cachedMacro op.CallOp

	linearSaturations []uint8
	rankedSaturations []uint8
}

func NewHeatMap(xBucketSize time.Duration, yBucketSize int, maxY int, data [][]int) *Heatmap {
	// the data argument consists of slices of values that were bucketed in the X dimension.

	// XXX guard against empty data, zero steps, etc

	if debug {
		for i, row := range data {
			if len(row) != len(data[0]) {
				panic("all rows must have same size")
			}

			for j, y := range row {
				if y > maxY {
					panic(fmt.Sprintf("value %d at row %d column %d exceeds maximum of %d", y, i, j, maxY))
				}
			}
		}
	}

	hm := &Heatmap{
		xBucketSize: xBucketSize,
		YBucketSize: yBucketSize,
		numXBuckets: len(data[0]),
		maxY:        maxY,
		origData:    data,
	}
	hm.computeBuckets()
	hm.computeSaturations()
	return hm
}

func (hm *Heatmap) computeBuckets() {
	hm.numYBuckets = int(math.Ceil(float64(hm.maxY) / float64(hm.YBucketSize)))
	hm.data = make([]int, hm.numXBuckets*hm.numYBuckets)
	for _, xBuckets := range hm.origData {
		for i, y := range xBuckets {
			bin := y / hm.YBucketSize
			if bin >= hm.numYBuckets {
				// Say we have a bin size of 10, a minimum value of 0 and a maximum value of 100. Then we will have bins
				// [0, 10), [10, 20), ..., [90, 100]. That is, the last bucket is right closed, to catch the final
				// value. Otherwise we would need [90, 100) and [100, 100], and that'd be weird.
				//
				// Technically, our final bucket captures in this example is [100, ∞], because we'd rather have a catch
				// all than compute an invalid index that may write to other bins, or go out of bounds.
				bin = hm.numYBuckets - 1
			}
			idx := i*hm.numYBuckets + bin
			hm.data[idx]++
		}
	}
}

func (hm *Heatmap) computeSaturations() {
	if len(hm.data) == 0 {
		return
	}

	sorted := make([]int, len(hm.data))
	copy(sorted, hm.data)
	sort.Ints(sorted)
	prev := -1
	// We can reuse sorted's backing storage
	unique := sorted[:0]
	for _, v := range sorted {
		if v == prev {
			continue
		}
		unique = append(unique, v)
		prev = v
	}

	hm.linearSaturations = make([]uint8, len(hm.data))
	hm.rankedSaturations = make([]uint8, len(hm.data))
	for i, v := range hm.data {
		// OPT(dh): surely there's a way to structure this algorithm that we don't have to search our position in
		// the slice of unique, sorted buckets
		satIdx := sort.SearchInts(unique, v)
		if satIdx == len(unique) {
			panic("couldn't find bucket")
		}
		s := uint8(0xFF * (float32(satIdx+1) / float32(len(unique))))
		if s == 0 {
			// Ensure non-zero value has non-zero saturation
			s = 1
		}
		hm.rankedSaturations[i] = s

		s = uint8(0xFF * (float32(v) / float32(sorted[len(sorted)-1])))
		if s == 0 {
			// Ensure non-zero value has non-zero saturation
			s = 1
		}
		hm.linearSaturations[i] = s
	}
}

type HeatmapBucket struct {
	XStart time.Duration
	XEnd   time.Duration
	YStart int
	YEnd   int
	Count  int
}

func (hm *Heatmap) HoveredBucket() (HeatmapBucket, bool) {
	return hm.hovered, hm.hovered.Count != -1
}

func (hm *Heatmap) Layout(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	// TODO(dh): add scrollable X axis

	dims := gtx.Constraints.Max
	for _, e := range gtx.Events(hm) {
		ev := e.(pointer.Event)
		hm.pointer = ev.Position
		hm.pointerConstraint = dims
	}

	numXBuckets := len(hm.data) / hm.numYBuckets
	xStepPx := float32(dims.X) / float32(numXBuckets)
	yStepPx := float32(dims.Y) / float32(hm.numYBuckets)

	key := heatmapCacheKey{
		size:            dims,
		useLinearColors: hm.UseLinearColors,
		yBucketSize:     hm.YBucketSize,
	}
	if hm.cacheKey == key {
		hm.cachedMacro.Add(gtx.Ops)
	} else {
		hm.cacheKey = key
		hm.cachedOps.Reset()
		m := op.Record(&hm.cachedOps)

		stack := clip.Rect{Max: dims}.Push(&hm.cachedOps)
		pointer.InputOp{Tag: hm, Types: pointer.Move}.Add(&hm.cachedOps)

		max := 0
		for _, v := range hm.data {
			if v > max {
				max = v
			}
		}

		// As per usual, batching draw calls hugely increases performance. Instead of thousands of draw calls, this caps us
		// at 256 draw calls, one per possible saturation.
		//
		// We don't bother reusing op.Ops or clip.Paths for now. We only hit this code when the window size has changed.
		// Otherwise we just reuse the previous frame's final output.
		var ops [256]op.Ops
		var paths [256]clip.Path
		for i := range paths {
			paths[i].Begin(&ops[i])
		}

		var saturations []uint8
		if hm.UseLinearColors {
			saturations = hm.linearSaturations
		} else {
			saturations = hm.rankedSaturations
		}

		for x := 0; x < numXBuckets; x++ {
			for y := 0; y < hm.numYBuckets; y++ {
				idx := x*hm.numYBuckets + y
				v := hm.data[idx]
				if v == 0 {
					// Don't explicitly draw rectangles for empty buckets. This is an optimization.
					continue
				}

				xStart := float32(x) * xStepPx
				yEnd := float32(dims.Y) - float32(y)*yStepPx
				xEnd := xStart + xStepPx
				yStart := yEnd - yStepPx

				p := &paths[saturations[idx]]
				p.MoveTo(f32.Pt(float32(xStart), float32(yStart)))
				p.LineTo(f32.Pt(float32(xEnd), float32(yStart)))
				p.LineTo(f32.Pt(float32(xEnd), float32(yEnd)))
				p.LineTo(f32.Pt(float32(xStart), float32(yEnd)))
				p.Close()
			}
		}

		for i := range paths {
			// TODO(dh): we shouldn't assume that the background color is (60, 0.082, 1), it might be configurable in the future.
			//
			// Our background color isn't pure white, it has a yellowish tint. If we just took our heatmap's color and
			// changed the saturation, then lighter values would look increasingly off. Instead, we interpolate between
			// both the hue (60 to 0, yellow to red) and the saturation (0.08 to 1).
			f := float32(i) / 255
			// Red is 0°, yellow is 60°. The fuller the bucket, the redder the value, so interpolate from 60 to 0.
			h := lerp(60, 0, f)
			// The fuller the bucket, the more saturated the color. Interpolate from 0.082 to 1, where 0.082 is the
			// saturation of our yellowish background color.
			s := lerp(0.082, 1, f)

			c := hsvToRgb(h, s, 1)
			paint.FillShape(&hm.cachedOps, c, clip.Outline{Path: paths[i].End()}.Op())
		}

		// XXX we still have gaps between rectangles sometimes. god damn floating point imprecision. do we wanna go back
		// to integer corodinates?

		stack.Pop()
		hm.cachedMacro = m.Stop()

		hm.cachedMacro.Add(gtx.Ops)
	}

	if hm.pointerConstraint == dims && hm.pointer.X > 0 && hm.pointer.Y > 0 && hm.pointer.X <= float32(dims.X) && hm.pointer.Y <= float32(dims.Y) {
		x := int(hm.pointer.X / xStepPx)
		y := int((float32(dims.Y) - hm.pointer.Y) / yStepPx)

		xStart := float32(x) * xStepPx
		yEnd := float32(dims.Y) - float32(y)*yStepPx
		xEnd := xStart + xStepPx
		yStart := yEnd - yStepPx

		stroke := clip.Stroke{
			Path:  FRect{Min: f32.Pt(xStart, yStart), Max: f32.Pt(xEnd, yEnd)}.Path(gtx.Ops),
			Width: float32(gtx.Dp(1)),
		}.Op()
		// XXX use constant or theme for the color
		paint.FillShape(gtx.Ops, rgba(0x0000FFFF), stroke)

		idx := x*hm.numYBuckets + y
		hm.hovered = HeatmapBucket{
			XStart: time.Duration(x) * hm.xBucketSize,
			XEnd:   time.Duration(x)*hm.xBucketSize + hm.xBucketSize,
			YStart: y * hm.YBucketSize,
			YEnd:   y*hm.YBucketSize + hm.YBucketSize,
			Count:  hm.data[idx],
		}
	} else {
		hm.hovered = HeatmapBucket{Count: -1}
	}

	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func lerp(start, end, ratio float32) float32 {
	return (1-ratio)*start + ratio*end
}

func hsvToRgb(h float32, s float32, v float32) color.NRGBA {
	c := v * s
	h_ := h / 60
	x := c * (1 - float32(math.Abs(math.Mod(float64(h_), 2)-1)))

	var r, g, b float32
	switch int(h_) % 6 {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	case 5:
		r, g, b = c, 0, x
	default:
		panic("unreachable")
	}

	m := v - c
	r += m
	g += m
	b += m

	return color.NRGBA{uint8(round(r * 255)), uint8(round(g * 255)), uint8(round(b * 255)), 0xFF}
}

func round(x float32) float32 {
	return float32(math.Round(float64(x)))
}

type HeatmapWindow struct {
	theme *theme.Theme
	trace *Trace
}

func (hwin *HeatmapWindow) Run(win *app.Window) error {
	xStep := 100 * time.Millisecond
	yStep := 1
	buckets := make([][]int, len(hwin.trace.ps))
	for i, p := range hwin.trace.ps {
		buckets[i] = computeProcessorBusy(hwin.trace, p, xStep)
	}
	hm := NewHeatMap(xStep, yStep, 100, buckets)

	var useLinear widget.Bool
	var ops op.Ops
	for e := range win.Events() {
		switch ev := e.(type) {
		case system.DestroyEvent:
			return ev.Err
		case system.FrameEvent:
			gtx := layout.NewContext(&ops, ev)
			gtx.Constraints.Min = image.Point{}
			paint.Fill(gtx.Ops, colors[colorBackground])

			if useLinear.Changed() {
				hm.UseLinearColors = useLinear.Value
			}

			for _, e := range gtx.Events(hwin) {
				if ev, ok := e.(key.Event); ok && ev.State == key.Press {
					// TODO(dh): provide visual feedback, displaying the bucket size
					switch ev.Name {
					case "↑":
						hm.YBucketSize++
						hm.computeBuckets()
						hm.computeSaturations()
					case "↓":
						hm.YBucketSize--
						if hm.YBucketSize < 1 {
							hm.YBucketSize = 1
						}
						hm.computeBuckets()
						hm.computeSaturations()
					}
				}
			}

			key.InputOp{Tag: hwin, Keys: "↑|↓"}.Add(gtx.Ops)
			key.FocusOp{Tag: hwin}.Add(gtx.Ops)

			layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return hm.Layout(gtx, hwin.theme)
				}),
				// TODO(dh): add some padding between elements
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					var label string

					if b, ok := hm.HoveredBucket(); ok {
						label = local.Sprintf("time %s, range %d–%d, count: %d", b.XStart, b.YStart, b.YEnd, b.Count)
					}
					return mywidget.TextLine{Color: hwin.theme.Palette.Foreground}.Layout(gtx, hwin.theme.Shaper, text.Font{}, hwin.theme.TextSize, label)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// TODO(dh): instead of using a checkbox, use a toggle switch that shows the two options (linear and
					// ranked). With the checkbox, the user doesn't know what's being used when the checkbox isn't
					// ticked.
					return theme.CheckBox(hwin.theme, &useLinear, "Use linear saturation").Layout(gtx)
				}),
			)

			ev.Frame(gtx.Ops)
		}
	}

	return nil
}

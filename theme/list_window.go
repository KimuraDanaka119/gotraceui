package theme

import (
	"context"
	"fmt"
	"image/color"
	rtrace "runtime/trace"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"
	"gioui.org/x/eventx"
	mywidget "honnef.co/go/gotraceui/widget"
)

type listWindowItem[T any] struct {
	index int
	item  T
	s     string
	click widget.Clickable
}

type Filter[T any] interface {
	Filter(item T) bool
}

type ListWindow[T fmt.Stringer] struct {
	BuildFilter func(string) Filter[T]

	items []listWindowItem[T]

	filtered []int
	// index of the selected item in the filtered list
	index     int
	done      bool
	cancelled bool

	theme *Theme
	input widget.Editor
	list  widget.List
}

func NewListWindow[T fmt.Stringer](th *Theme) *ListWindow[T] {
	return &ListWindow[T]{
		theme: th,
		input: widget.Editor{
			SingleLine: true,
			Submit:     true,
		},
		list: widget.List{
			List: layout.List{
				Axis: layout.Vertical,
			},
		},
	}
}

func (w *ListWindow[T]) SetItems(items []T) {
	w.items = make([]listWindowItem[T], len(items))
	w.filtered = make([]int, len(items))
	for i, item := range items {
		w.items[i] = listWindowItem[T]{
			item:  item,
			index: i,
			s:     item.String(),
		}
		w.filtered[i] = i
	}
}

func (w *ListWindow[T]) Cancelled() bool { return w.cancelled }
func (w *ListWindow[T]) Confirmed() (T, bool) {
	if !w.done {
		var zero T
		return zero, false
	}
	w.done = false
	return w.items[w.filtered[w.index]].item, true
}

func (w *ListWindow[T]) Layout(gtx layout.Context) layout.Dimensions {
	defer rtrace.StartRegion(context.Background(), "theme.ListWindow.Layout").End()
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()

	key.InputOp{Tag: w, Keys: "↓|↑|⎋"}.Add(gtx.Ops)

	var spy *eventx.Spy

	dims := mywidget.Bordered{Color: w.theme.Palette.WindowBorder, Width: w.theme.WindowBorder}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
		spy, gtx = eventx.Enspy(gtx)
		gtx.Constraints.Min.X = gtx.Constraints.Max.X

		paint.Fill(gtx.Ops, w.theme.Palette.Background)

		fn2 := func(gtx layout.Context) layout.Dimensions {
			return List(w.theme, &w.list).Layout(gtx, len(w.filtered), func(gtx layout.Context, index int) layout.Dimensions {
				// XXX use constants for colors
				item := &w.items[w.filtered[index]]
				return item.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					var c color.NRGBA
					if index == w.index {
						// XXX make this pretty, don't just change the font color
						c = rgba(0xFF0000FF)
					} else if item.click.Hovered() {
						// XXX make this pretty, don't just change the font color
						c = rgba(0xFF00FFFF)
					} else {
						c = rgba(0x000000FF)
					}
					return mywidget.TextLine{Color: c}.Layout(gtx, w.theme.Shaper, text.Font{}, w.theme.TextSize, item.s)
				})
			})
		}

		flex := layout.Flex{
			Axis: layout.Vertical,
		}
		editor := Editor(w.theme, &w.input, "")
		editor.Editor.Focus()
		return flex.Layout(gtx, layout.Rigid(editor.Layout), layout.Flexed(1, fn2))
	})

	// The editor widget selectively handles the up and down arrow keys, depending on the contents of the text field and
	// the position of the cursor. This means that our own InputOp won't always be getting all events. But due to the
	// selectiveness of the editor's InputOp, we can't fully rely on it, either. We need to combine the events of the
	// two.
	//
	// To be consistent, we handle all events after layout of the nested widgets, to have the same frame latency for all
	// events.
	handleKey := func(ev key.Event) {
		if ev.State == key.Press {
			firstVisible := w.list.Position.First
			lastVisible := w.list.Position.First + w.list.Position.Count - 1
			if w.list.Position.Offset > 0 {
				// The last element might be barely visible, even just one pixel. and we still want to scroll in that
				// case
				firstVisible++
			}
			if w.list.Position.OffsetLast < 0 {
				// The last element might be barely visible, even just one pixel. and we still want to scroll in that
				// case
				lastVisible--
			}
			visibleCount := lastVisible - firstVisible + 1

			switch ev.Name {
			case "↑":
				w.index--
				if w.index < firstVisible {
					// XXX compute the correct position. the user might have scrolled the list via its scrollbar.
					w.list.Position.First--
				}
				if w.index < 0 {
					w.index = len(w.filtered) - 1
					w.list.Position.First = w.index - visibleCount + 1
				}
			case "↓":
				w.index++
				if w.index > lastVisible {
					// XXX compute the correct position. the user might have scrolled the list via its scrollbar.
					w.list.Position.First++
				}
				if w.index >= len(w.filtered) {
					w.index = 0
					w.list.Position.First = 0
					w.list.Position.Offset = 0
				}
			case "⎋": // Escape
				w.cancelled = true
			}
		}
	}
	for _, evs := range spy.AllEvents() {
		for _, ev := range evs.Items {
			if ev, ok := ev.(key.Event); ok {
				handleKey(ev)
			}
		}
	}
	for _, ev := range w.input.Events() {
		switch ev.(type) {
		case widget.ChangeEvent:
			w.filtered = w.filtered[:0]
			f := w.BuildFilter(w.input.Text())
			for _, item := range w.items {
				if f.Filter(item.item) {
					w.filtered = append(w.filtered, item.index)
				}
			}
			// TODO(dh): if the previously selected entry hasn't been filtered away, then it should stay selected.
			if w.index >= len(w.filtered) {
				// XXX if there are no items, then this sets w.index to -1, causing two bugs: hitting return will panic,
				// and once there are items again, none of them will be selected
				w.index = len(w.filtered) - 1
			}
		case widget.SubmitEvent:
			if len(w.filtered) != 0 {
				w.done = true
			}
		}
	}
	for i, idx := range w.filtered {
		if w.items[idx].click.Clicked() {
			w.index = i
			w.done = true
		}
	}

	for _, ev := range gtx.Events(w) {
		switch ev := ev.(type) {
		case key.Event:
			handleKey(ev)
		}
	}

	return dims
}

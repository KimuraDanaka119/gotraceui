package theme

import (
	"gioui.org/layout"
	"gioui.org/text"
	"honnef.co/go/gotraceui/widget"
)

type TableListColumn struct {
	Name     string
	MinWidth int
	MaxWidth int
}

type TableListStyle struct {
	Columns []TableListColumn
	List    *widget.List
}

func (tbl *TableListStyle) Layout(
	win *Window,
	gtx layout.Context,
	numItems int,
	cellFn func(gtx layout.Context, row, col int) layout.Dimensions,
) layout.Dimensions {
	st := List(win.Theme, tbl.List)
	st.EnableCrossScrolling = true

	ourCellFn := func(gtx layout.Context, row, col int) layout.Dimensions {
		if row == 0 {
			return widget.TextLine{Color: win.Theme.Palette.Foreground}.
				Layout(gtx, win.Theme.Shaper, text.Font{Weight: text.Bold}, win.Theme.TextSize, tbl.Columns[col].Name)
		} else {
			return cellFn(gtx, row-1, col)
		}
	}

	return st.Layout(gtx, numItems+1, func(gtx layout.Context, index int) layout.Dimensions {
		rigids := make([]layout.FlexChild, len(tbl.Columns))

		for i, col := range tbl.Columns {
			i := i
			col := col
			rigids[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if col.MinWidth != 0 {
					gtx.Constraints.Min.X = col.MinWidth
				}
				if col.MaxWidth != 0 {
					gtx.Constraints.Max.X = col.MaxWidth
				}

				return ourCellFn(gtx, index, i)
			})
		}

		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, rigids...)
	})
}

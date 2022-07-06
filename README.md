# gotraceui - an efficient frontend for Go execution traces

gotraceui will be a frontend for Go execution traces. Currently it is a mere prototype and not yet useful.

## Use

There's no way to productively use it yet, but if you want to run it, anyway: the command takes a single argument, a
path to a Go execution trace, like generated by `go test -trace`. Some samples exist in `./trace/testdata`.

## Building

See https://gioui.org/doc/install to find the per-OS build requirements. Good luck.

## Controls

None of these controls are final. Users without a middle mouse button will have a bad experience right now.

| Key                         | Function                                                                    |
|-----------------------------|-----------------------------------------------------------------------------|
| Middle mouse button (hold)  | Pan the view                                                                |
| Shift + middle mouse button | Draw a zoom selection                                                       |
| Ctrl + middle mouse button  | Zoom to clicked span or goroutine                                           |
| Scroll wheel                | Zoom in and out                                                             |
| Home                        | Scroll to top of goroutine list                                             |
| Ctrl + Home                 | Zooms to fit current goroutines                                             |
| Shift + Home                | Jump to timestamp 0                                                         |
| X                           | Toggle display of all goroutine labels                                      |
| C                           | Toggle compact display                                                      |
| G                           | Open a goroutine selector                                                   |
| T                           | Toggle displaying tooltips; only spans -> none -> both spans and goroutines |
| E                           | Toggle highlighting spans with events                                       |

## Notes

No aspect of gotraceui is final yet, but do note that bright pink and bright yellow are debug colors and I never thought
they were a good idea. The rest of the color scheme is actually meant to be pleasant.

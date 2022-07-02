# gotraceui - an efficient frontend for Go execution traces

gotraceui will be a frontend for Go execution traces. Currently it is a mere prototype and not yet useful.

## Use

There's no way to productively use it yet, but if you want to run it, anyway: the command takes a single argument, a
path to a Go execution trace, like generated by `go test -trace`. Some samples exist in `./trace/testdata`.

## Controls

| Key                         | Function                        |
|-----------------------------+---------------------------------|
| Middle mouse button (hold)  | Pan the view                    |
| Shift + middle mouse button | Draw a zoom selection           |
| Ctrl + middle mouse button  | Zoom to clicked span            |
| Scroll wheel                | Zoom in and out                 |
| Home                        | Scroll to top of goroutine list |
| Shift + Home                | Zooms to fit current goroutines |
| Ctrl + Home                 | Jump to timestamp 0             |


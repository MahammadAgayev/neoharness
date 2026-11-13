module github.com/MahammadAgayev/neoharness/tui

go 1.26.2

require (
	github.com/MahammadAgayev/neoharness/api v0.0.0
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/gdamore/tcell/v2 v2.13.10
	github.com/rivo/uniseg v0.4.7
	github.com/yuin/goldmark v1.8.7
	github.com/yuin/gopher-lua v1.1.2
)

require (
	github.com/dlclark/regexp2/v2 v2.2.1 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/term v0.37.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)

replace github.com/MahammadAgayev/neoharness/api => ../api

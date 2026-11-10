package tui

import "slices"

// Rect is a screen area.
type Rect struct{ X, Y, W, H int }

func (r Rect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Window shows one buffer, with its own cursor and scroll.
type Window struct {
	Buf *Buffer
	Alt *Buffer
	// Top is the first visible line, Cur the cursor line, Col the cursor
	// column in cells (prefix included).
	Top, Cur, Col int
	// Follow keeps the cursor on the last line as lines are appended.
	Follow bool
	// Visual selection: mode is 0, 'v' or 'V'; the anchor is where it began.
	vmode         rune
	vLine, vCol   int
	rect          Rect
	textH, linesN int
	// jumps are the buffers the window showed before, the newest last, for
	// <C-o>; views are where the window was in each.
	jumps []*Buffer
	views map[*Buffer]view
	// inPrompt says the Normal cursor is in a chat's prompt (prompt.go).
	inPrompt         bool
	chatCur, chatCol int
}

// outOfPrompt puts Cur and Col back on the transcript.
func (w *Window) outOfPrompt() {
	if w.inPrompt {
		w.Cur, w.Col, w.inPrompt = w.chatCur, w.chatCol, false
	}
}

type view struct {
	top, cur, col int
	follow        bool
}

const maxJumps = 50

func newWindow(b *Buffer) *Window { return &Window{Buf: b, Follow: true} }

// show switches the window to another buffer, remembering the old one for
// <C-^> and <C-o>.
func (w *Window) show(b *Buffer) {
	if b == w.Buf {
		return
	}
	w.jumps = append(slices.DeleteFunc(w.jumps, func(j *Buffer) bool { return j == w.Buf || j == b }), w.Buf)
	if len(w.jumps) > maxJumps {
		w.jumps = w.jumps[1:]
	}
	w.swap(b)
}

// back is <C-o>: the window shows the buffer it showed before.
func (w *Window) back() bool {
	if len(w.jumps) == 0 {
		return false
	}
	b := w.jumps[len(w.jumps)-1]
	w.jumps = w.jumps[:len(w.jumps)-1]
	w.swap(b)
	return true
}

// swap shows b where the window last was in it.
func (w *Window) swap(b *Buffer) {
	w.outOfPrompt()
	if w.views == nil {
		w.views = map[*Buffer]view{}
	}
	w.views[w.Buf] = view{w.Top, w.Cur, w.Col, w.Follow}
	w.Alt = w.Buf
	w.Buf = b
	w.vmode = 0
	if v, ok := w.views[b]; ok {
		w.Top, w.Cur, w.Col, w.Follow = v.top, v.cur, v.col, v.follow
		return
	}
	w.Top, w.Cur, w.Col = 0, 0, 0
	// A chat follows its end; help and artifacts read from the top.
	w.Follow = b.Kind != KindHelp && b.Kind != KindArtifact
}

// forget drops a buffer that is gone from the window's history.
func (w *Window) forget(b *Buffer) {
	if w.Alt == b {
		w.Alt = nil
	}
	w.jumps = slices.DeleteFunc(w.jumps, func(j *Buffer) bool { return j == b })
	delete(w.views, b)
}

// node is a split tree: a leaf holds a window; an inner node lays its kids
// out left to right (vert) or top to bottom.
type node struct {
	vert   bool
	kids   []*node
	weight float64
	win    *Window
	parent *node
}

func leaf(w *Window, weight float64) *node { return &node{win: w, weight: weight} }

func split(vert bool, kids ...*node) *node {
	n := &node{vert: vert, weight: 1}
	for _, k := range kids {
		k.parent = n
		n.kids = append(n.kids, k)
	}
	return n
}

// layout assigns every window its area. sep is called for each separator
// column between side-by-side kids.
func (n *node) layout(r Rect, sep func(x, y, h int)) {
	if n.win != nil {
		n.win.rect = r
		return
	}
	total := 0.0
	for _, k := range n.kids {
		total += k.weight
	}
	if n.vert {
		avail := r.W - (len(n.kids) - 1)
		x := r.X
		for i, k := range n.kids {
			w := int(float64(avail) * k.weight / total)
			if i == len(n.kids)-1 {
				w = r.X + r.W - x
			}
			k.layout(Rect{x, r.Y, w, r.H}, sep)
			x += w
			if i < len(n.kids)-1 {
				sep(x, r.Y, r.H)
				x++
			}
		}
		return
	}
	y := r.Y
	for i, k := range n.kids {
		h := int(float64(r.H) * k.weight / total)
		if i == len(n.kids)-1 {
			h = r.Y + r.H - y
		}
		k.layout(Rect{r.X, y, r.W, h}, sep)
		y += h
	}
}

func (n *node) windows() []*Window {
	if n.win != nil {
		return []*Window{n.win}
	}
	var out []*Window
	for _, k := range n.kids {
		out = append(out, k.windows()...)
	}
	return out
}

func (n *node) find(w *Window) *node {
	if n.win == w {
		return n
	}
	for _, k := range n.kids {
		if f := k.find(w); f != nil {
			return f
		}
	}
	return nil
}

// splitWindow puts nw right of w (vert) or above it, sharing w's space: the
// user's Neovim has splitright and nosplitbelow.
func (n *node) splitWindow(w, nw *Window, vert bool) {
	l := n.find(w)
	if l == nil {
		return
	}
	p := l.parent
	if p != nil && p.vert == vert {
		l.weight /= 2
		nl := leaf(nw, l.weight)
		nl.parent = p
		for i, k := range p.kids {
			if k == l {
				if vert {
					i++
				}
				p.kids = append(p.kids[:i], append([]*node{nl}, p.kids[i:]...)...)
				break
			}
		}
		return
	}
	// Turn the leaf into an inner node holding the old window and the new.
	old := leaf(w, 1)
	nl := leaf(nw, 1)
	l.win = nil
	l.vert = vert
	old.parent, nl.parent = l, l
	l.kids = []*node{nl, old}
	if vert {
		l.kids = []*node{old, nl}
	}
}

// closeWindow removes w and gives its space to one neighbor, as Neovim does
// with splitright and nosplitbelow: the window on the left, or the one
// below. It returns the window that got the space, nil for the last window,
// and whether the space was a width.
func (n *node) closeWindow(w *Window) (*Window, bool) {
	l := n.find(w)
	if l == nil || l.parent == nil {
		return nil, false
	}
	p := l.parent
	vert := p.vert
	var to *node
	for i, k := range p.kids {
		if k == l {
			if i+1 < len(p.kids) && (!p.vert || i == 0) {
				to = p.kids[i+1]
			} else {
				to = p.kids[i-1]
			}
			p.kids = append(p.kids[:i], p.kids[i+1:]...)
			break
		}
	}
	to.weight += l.weight
	if len(p.kids) == 1 {
		// Collapse: the parent becomes its only kid.
		only := p.kids[0]
		p.win, p.vert, p.kids = only.win, only.vert, only.kids
		for _, k := range p.kids {
			k.parent = p
		}
		to = p
	}
	return to.windows()[0], vert
}

// count is how many windows are side by side in n (vert) or one above the
// other.
func (n *node) count(vert bool) int {
	if n.win != nil {
		return 1
	}
	c := 0
	for _, k := range n.kids {
		if n.vert == vert {
			c += k.count(vert)
		} else {
			c = max(c, k.count(vert))
		}
	}
	return c
}

// equalize gives every window the same width (vert) or height. As in
// Neovim, a kid's share is its count of windows, not one share per kid.
func (n *node) equalize(vert bool) {
	for _, k := range n.kids {
		if n.vert == vert {
			k.weight = float64(k.count(vert))
		}
		k.equalize(vert)
	}
}

// neighbor finds the window next to w in a direction: 'h', 'j', 'k', 'l'.
func neighbor(wins []*Window, w *Window, dir rune) *Window {
	r := w.rect
	cx, cy := r.X+min(w.Col, r.W-1), r.Y+min(w.Cur-w.Top, r.H-1)
	var best *Window
	bestD := 1 << 30
	for _, o := range wins {
		if o == w {
			continue
		}
		q := o.rect
		var d int
		switch dir {
		case 'h':
			if q.X+q.W > r.X || cy < q.Y || cy >= q.Y+q.H {
				continue
			}
			d = r.X - (q.X + q.W)
		case 'l':
			if q.X < r.X+r.W || cy < q.Y || cy >= q.Y+q.H {
				continue
			}
			d = q.X - (r.X + r.W)
		case 'k':
			if q.Y+q.H > r.Y || cx < q.X || cx >= q.X+q.W {
				continue
			}
			d = r.Y - (q.Y + q.H)
		case 'j':
			if q.Y < r.Y+r.H || cx < q.X || cx >= q.X+q.W {
				continue
			}
			d = q.Y - (r.Y + r.H)
		}
		if d < bestD {
			best, bestD = o, d
		}
	}
	return best
}

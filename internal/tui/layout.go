package tui

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
}

func newWindow(b *Buffer) *Window { return &Window{Buf: b, Follow: true} }

// show switches the window to another buffer, remembering the old one for
// <C-^>.
func (w *Window) show(b *Buffer) {
	if b == w.Buf {
		return
	}
	w.Alt = w.Buf
	w.Buf = b
	w.Top, w.Cur, w.Col = 0, 0, 0
	w.Follow = true
	w.vmode = 0
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

// closeWindow removes w. It reports false for the last window.
func (n *node) closeWindow(w *Window) bool {
	l := n.find(w)
	if l == nil || l.parent == nil {
		return false
	}
	p := l.parent
	for i, k := range p.kids {
		if k == l {
			p.kids = append(p.kids[:i], p.kids[i+1:]...)
			break
		}
	}
	if len(p.kids) == 1 {
		// Collapse: the parent becomes its only kid.
		only := p.kids[0]
		p.win, p.vert, p.kids = only.win, only.vert, only.kids
		for _, k := range p.kids {
			k.parent = p
		}
	}
	return true
}

func (n *node) equalize() {
	n.weight = 1
	for _, k := range n.kids {
		k.equalize()
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

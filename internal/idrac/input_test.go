package idrac

import "testing"

// The graphical client reports pointer samples in the window rectangle it
// draws into, which is almost never the size of the framebuffer. Dropping the
// viewport size used to send those coordinates unchanged, so the remote cursor
// matched in the top left corner and drifted further apart the further the
// pointer moved away from it.
func TestScaleAxisMapsAViewportOntoTheFramebuffer(t *testing.T) {
	cases := []struct {
		name string
		v    int
		from int
		to   int
		want int
	}{
		{name: "same size passes through", v: 417, from: 1024, to: 1024, want: 417},
		{name: "origin stays at the origin", v: 0, from: 800, to: 1920, want: 0},
		{name: "last column reaches the last column", v: 799, from: 800, to: 1920, want: 1919},
		{name: "the middle stays in the middle", v: 400, from: 800, to: 1920, want: 961},
		{name: "half size doubles", v: 100, from: 512, to: 1024, want: 200},
		{name: "double size halves", v: 200, from: 2048, to: 1024, want: 100},
		{name: "beyond the viewport clamps", v: 5000, from: 800, to: 1920, want: 1919},
		{name: "negative clamps", v: -3, from: 800, to: 1920, want: 0},
		{name: "unknown viewport passes through", v: 640, from: 0, to: 1920, want: 640},
		{name: "unknown framebuffer passes through", v: 640, from: 800, to: 0, want: 640},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaleAxis(tc.v, tc.from, tc.to); got != tc.want {
				t.Fatalf("scaleAxis(%d, %d, %d) = %d, want %d", tc.v, tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// Scaling has to stay monotonic and stay inside the framebuffer for every
// sample a viewport can produce, otherwise a drag would jump backwards
// somewhere along the way.
func TestScaleAxisStaysInsideAndOrdered(t *testing.T) {
	const from, to = 1371, 1920
	previous := -1
	for v := 0; v < from; v++ {
		got := scaleAxis(v, from, to)
		if got < 0 || got >= to {
			t.Fatalf("scaleAxis(%d, %d, %d) = %d, outside the framebuffer", v, from, to, got)
		}
		if got < previous {
			t.Fatalf("scaleAxis(%d) = %d went backwards from %d", v, got, previous)
		}
		previous = got
	}
}

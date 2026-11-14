package desktopui

import (
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/nsgotest"
)

// rightEdge is where a view ends in window points, as its ancestors and their scroll offsets place it.
func rightEdge(v *nsgotest.View) float32 {
	x := float32(v.Frame.Origin.X + v.Frame.Size.Width)
	for p := v.Parent; p != nil; p = p.Parent {
		x += float32(p.Frame.Origin.X - p.Offset.X)
	}
	return x
}

func TestTitleBarFitsAtTheMinimumWidth(t *testing.T) {
	application := startPages(t, pageFixture(), false)
	limits := mainWindowConstraints()
	application.Resize(limits.MinWidth, 760)
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:open-settings") })
	settings := application.Find("id:open-settings")
	title := application.Find("text:Claude Desktop Profiles")
	t.Logf("title %.0f wide ends at %.0f; settings ends at %.0f of %.0f", title.Frame.Size.Width, rightEdge(title),
		rightEdge(settings), limits.MinWidth)
	if edge := rightEdge(settings); edge > float32(limits.MinWidth)-8 {
		t.Fatalf("settings button ends at %.0f, past the %.0f-point window", edge, limits.MinWidth)
	}
	if title.Text != "Claude Desktop Profiles" || title.Frame.Size.Width < 140 {
		t.Fatalf("title squeezed to %.0f points", title.Frame.Size.Width)
	}
}

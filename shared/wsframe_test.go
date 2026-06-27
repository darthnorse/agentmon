package shared

import (
	"encoding/json"
	"testing"
)

func TestResizeFrameRoundTrip(t *testing.T) {
	in := ResizeFrame{Type: FrameResize, Cols: 88, Rows: 26}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ResizeFrame
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip: %+v != %+v", out, in)
	}
	if string(b) != `{"type":"resize","cols":88,"rows":26}` {
		t.Fatalf("wire shape: %s", b)
	}
}

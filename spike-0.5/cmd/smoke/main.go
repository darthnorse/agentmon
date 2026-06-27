// Smoke client for the spike: connects to the live HTTPS WebSocket, reads the
// scrollback snapshot, sends a resize and a literal input probe. Test-only.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := flag.String("url", "wss://127.0.0.1:8443/ws", "ws url")
	token := flag.String("token", "", "token")
	probe := flag.String("probe", "WSPROBE", "literal input to inject")
	flag.Parse()

	d := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	c, _, err := d.Dial(*url+"?token="+*token, nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// first inbound frame should be the scrollback snapshot
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := c.ReadMessage()
	if err != nil {
		log.Fatalf("read snapshot: %v", err)
	}
	fmt.Printf("SNAPSHOT: type=%d bytes=%d\n", mt, len(data))
	if len(data) > 0 {
		fmt.Printf("SNAPSHOT_HAS_CLAUDE=%v\n", contains(data, "Claude Code"))
	}

	// resize, then inject a literal probe over the binary channel
	_ = c.WriteMessage(websocket.TextMessage, mustJSON(map[string]any{
		"type": "resize", "cols": 90, "rows": 35,
	}))
	time.Sleep(200 * time.Millisecond)
	_ = c.WriteMessage(websocket.BinaryMessage, []byte(*probe))
	time.Sleep(500 * time.Millisecond)
	fmt.Println("SENT resize(90x35) + probe; closing")
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func contains(b []byte, s string) bool {
	return len(b) >= len(s) && indexOf(b, s) >= 0
}
func indexOf(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}

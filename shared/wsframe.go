package shared

const (
	FrameResize    = "resize"
	FrameError     = "error"
	FrameReconnect = "reconnect"
)

type ResizeFrame struct {
	Type string `json:"type"` // "resize"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type ErrorFrame struct {
	Type    string `json:"type"` // "error"
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReconnectFrame struct {
	Type   string `json:"type"` // "reconnect"
	Status string `json:"status"`
}

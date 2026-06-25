package output

import (
	"encoding/json"
	"io"

	"github.com/maguroid/llmx/internal/chat"
)

type JSONResponse struct {
	Content      string      `json:"content"`
	Model        string      `json:"model"`
	Usage        *chat.Usage `json:"usage"`
	FinishReason string      `json:"finish_reason"`
}

func JSON(w io.Writer, response JSONResponse) error {
	enc := json.NewEncoder(w)
	return enc.Encode(response)
}

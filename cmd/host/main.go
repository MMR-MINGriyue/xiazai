package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/pkg/browser"
)

type Message struct {
	Method string          `json:"method"`
	Meta   map[string]any  `json:"meta"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	Code    int    `json:"code"`
	Data    any    `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

func check() (data bool, err error) {
	conn, err := Dial()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	return true, nil
}

func wakeup(hidden bool) error {
	running, _ := check()
	if running {
		return nil
	}

	uri := "gopeed:"
	if hidden {
		uri = uri + "?hidden=true"
	}
	if err := browser.OpenURL(uri); err != nil {
		return err
	}

	for i := 0; i < 10; i++ {
		if ok, _ := check(); ok {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("start gopeed failed")
}

// postToFlutter sends a POST request to Flutter RPC server
func postToFlutter(path string, body []byte, headers map[string]string, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return Dial()
			},
		},
		Timeout: timeout,
	}
	req, err := http.NewRequest("POST", "http://127.0.0.1"+path, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}

// postToFlutterRetry 连接失败时唤醒应用并重试，降低“丢连接”概率。
// 重试次数默认 2 次，间隔 1s。
func postToFlutterRetry(path string, body []byte, headers map[string]string, timeout time.Duration, silent bool) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := postToFlutter(path, body, headers, timeout)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// 连接类失败：尝试重新拉起 gopeed 后重试
		if attempt < maxAttempts-1 {
			_ = wakeup(silent)
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return nil, fmt.Errorf("flutter rpc unavailable after %d attempts: %w", maxAttempts, lastErr)
}

// pingFlutter 健康检查（短超时），用于判断 host↔应用链路是否可用。
func pingFlutter() error {
	resp, err := postToFlutter("/health", []byte("{}"), nil, 2*time.Second)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

var apiMap = map[string]func(message *Message) (data any, err error){
	"ping": func(message *Message) (data any, err error) {
		// 先做命名管道连通性；若失败再尝试 health 端点（应用已起但 RPC 未 ready 的场景）
		if ok, _ := check(); ok {
			return true, nil
		}
		if err := pingFlutter(); err != nil {
			return false, err
		}
		return true, nil
	},
	"wakeup": func(message *Message) (data any, err error) {
		silent := false
		if v, ok := message.Meta["silent"]; ok {
			silent, _ = v.(bool)
		}
		err = wakeup(silent)
		return
	},
	"create": func(message *Message) (data any, err error) {
		buf, err := message.Params.MarshalJSON()
		if err != nil {
			return
		}

		silent := false
		if v, ok := message.Meta["silent"]; ok {
			silent, _ = v.(bool)
		}

		if err := wakeup(silent); err != nil {
			return nil, err
		}

		headers := make(map[string]string)
		if message.Meta != nil {
			metaJson, _ := json.Marshal(message.Meta)
			headers["X-Gopeed-Host-Meta"] = string(metaJson)
		}
		_, err = postToFlutterRetry("/create", buf, headers, 10*time.Second, silent)
		return
	},
	"forward": func(message *Message) (data any, err error) {
		buf, err := message.Params.MarshalJSON()
		if err != nil {
			return
		}

		silent := false
		if v, ok := message.Meta["silent"]; ok {
			silent, _ = v.(bool)
		}

		resp, err := postToFlutterRetry("/forward", buf, nil, 60*time.Second, silent)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		var respData map[string]json.RawMessage
		if err := json.Unmarshal(respBody, &respData); err != nil {
			return nil, err
		}
		return respData, nil
	},
}

// go build -ldflags="-s -w" -o ui/flutter/assets/exec/ github.com/GopeedLab/gopeed/cmd/host

func main() {
	for {
		// Read message length (first 4 bytes)
		var length uint32
		if err := binary.Read(os.Stdin, binary.NativeEndian, &length); err != nil {
			if err == io.EOF {
				return
			}
			sendError("Failed to read message length: " + err.Error())
			return
		}

		// Read the message
		input := make([]byte, length)
		if _, err := io.ReadFull(os.Stdin, input); err != nil {
			sendError("Failed to read message: " + err.Error())
			return
		}

		// Parse message
		var message Message
		if err := json.Unmarshal(input, &message); err != nil {
			sendError("Failed to parse message: " + err.Error())
			return
		}

		// Handle request
		var data any
		var err error
		if handler, ok := apiMap[message.Method]; ok {
			data, err = handler(&message)
		} else {
			err = errors.New("Unknown method: " + message.Method)
		}
		if err != nil {
			sendError(err.Error())
			continue
		}
		sendResponse(0, data, "")
	}
}

func sendResponse(code int, data interface{}, message string) {
	response := Response{
		Code:    code,
		Data:    data,
		Message: message,
	}

	// Encode response
	responseBytes, err := json.Marshal(response)
	if err != nil {
		sendError("Failed to encode response: " + err.Error())
		return
	}

	// Write message length
	binary.Write(os.Stdout, binary.NativeEndian, uint32(len(responseBytes)))
	// Write message
	os.Stdout.Write(responseBytes)
}

func sendError(msg string) {
	sendResponse(1, nil, msg)
}

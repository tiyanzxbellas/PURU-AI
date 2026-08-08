package health

import (
	"encoding/json"
	"net/http"
	"time"
)

func Serve(hostname string, port int) *http.Server {
	srv := &http.Server{
		Addr: hostname + ":" + itoa(port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"bot":       "PURU-AI",
				"running":   true,
				"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			})
		}),
	}
	return srv
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

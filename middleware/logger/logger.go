package logger

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const logTag = "[logger]"

// Log logs and records time taken by each requests. As a side effect,
// it trims the trailing slashes from the matched route.
func Log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		req.URL.Path = trimTrailingSlashes(req.URL.Path)
		next.ServeHTTP(w, req)
		log.Println(fmt.Sprintf("%s: finished %s, took %ds",
			logTag, fmt.Sprintf("%s %s", req.Method, req.URL.Path), time.Since(start)))
	})
}

func trimTrailingSlashes(path string) string {
	for path != "/" && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

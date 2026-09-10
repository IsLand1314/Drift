package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	for _, tc := range []struct {
		name       string
		args       []string
		configured bool
		code       int
		text       string
	}{
		{"success", []string{"-p", "hello"}, true, 0, "你好\n"},
		{"help", []string{"-h"}, false, 0, ""},
		{"missing config", []string{"-p", "hello"}, false, 2, ""},
		{"missing prompt", nil, true, 2, ""},
		{"unknown flag", []string{"-unknown"}, true, 2, ""},
		{"extra argument", []string{"-p", "hello", "extra"}, true, 2, ""},
		{"bad URL", []string{"-p", "hello", "-base-url", "file:///tmp"}, true, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if !tc.configured {
					return ""
				}
				return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
			}
			var out, stderr bytes.Buffer
			code := Run(context.Background(), tc.args, getenv, &out, &stderr)
			if code != tc.code || out.String() != tc.text {
				t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "test-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

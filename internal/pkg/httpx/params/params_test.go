package params

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newJSONContext(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx
}

func TestReadJSONAcceptsRootArray(t *testing.T) {
	var ids []int64

	if err := ReadJSON(newJSONContext(`[3,4]`), &ids); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}

	if len(ids) != 2 || ids[0] != 3 || ids[1] != 4 {
		t.Fatalf("expected ids [3 4], got %#v", ids)
	}
}

func TestReadStrictJSONContract(t *testing.T) {
	type payload struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"id":1,"name":"ok","extra":true}`},
		{name: "duplicate field", body: `{"id":1,"id":2,"name":"ok"}`},
		{name: "multiple values", body: `{"id":1,"name":"ok"}{"id":2,"name":"next"}`},
		{name: "trailing content", body: `{"id":1,"name":"ok"} trailing`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ReadStrictJSON(newJSONContext(test.body), &payload{}); err == nil {
				t.Fatalf("expected strict JSON rejection for %s", test.body)
			}
		})
	}

	decoded := payload{}
	if err := ReadStrictJSON(newJSONContext(`{"id":1,"name":"ok"}`), &decoded); err != nil {
		t.Fatalf("valid strict JSON rejected: %v", err)
	}
	if decoded.ID != 1 || decoded.Name != "ok" {
		t.Fatalf("unexpected decoded payload: %#v", decoded)
	}
}

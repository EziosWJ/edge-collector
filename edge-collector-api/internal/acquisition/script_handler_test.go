package acquisition

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestScriptHandlerUsesUnifiedEnvelopeAndEmptyRuntimeShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newScriptStoreFake()
	service, err := NewService(store, ScriptValidatorFunc(func(context.Context, string) (ScriptValidationResult, error) {
		return ScriptValidationResult{Valid: true, Errors: []ScriptValidationError{}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, NewCurrentStateStore())
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterRoutes(router, handler)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/scripts", bytes.NewBufferString(`{"name":"api-script","description":"demo","draftSource":"def after_poll(ctx):\n    pass\n"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Code int        `json:"code"`
		Data ScriptView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Code != 200 || created.Data.Name != "api-script" || created.Data.PublishedVersion != nil {
		t.Fatalf("create envelope = %+v", created)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/script-states", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("runtime state status = %d body=%s", response.Code, response.Body.String())
	}
	var states struct {
		Code int                  `json:"code"`
		Data []ScriptRuntimeState `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &states); err != nil {
		t.Fatal(err)
	}
	if states.Code != 200 || states.Data == nil || len(states.Data) != 0 {
		t.Fatalf("runtime state envelope = %+v", states)
	}
}

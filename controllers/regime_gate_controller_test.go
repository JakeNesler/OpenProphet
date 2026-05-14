package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"prophet-trader/services"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeRegimeGate satisfies the controller's status-source contract without
// touching disk. We don't reuse RegimeGateService because we want the test to
// fail when the controller stops calling GetStatus, not when the on-disk
// fixture format changes.
type fakeRegimeGate struct {
	status services.RegimeGateStatus
	calls  int
}

func (f *fakeRegimeGate) GetStatus() services.RegimeGateStatus {
	f.calls++
	return f.status
}

func TestRegimeGateController_HandleGetStatus_Returns200WithBody(t *testing.T) {
	// The HTTP endpoint must surface the service's status verbatim — agents
	// consume tier/sizing_multiplier/block_new_entries directly from this JSON.
	gin.SetMode(gin.TestMode)

	fake := &fakeRegimeGate{
		status: services.RegimeGateStatus{
			Score:            62,
			Tier:             "NORMAL",
			SizingMultiplier: 0.8,
			BlockNewEntries:  false,
		},
	}
	ctrl := NewRegimeGateController(fake)

	router := gin.New()
	router.GET("/api/v1/regime-gate/status", ctrl.HandleGetStatus)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/regime-gate/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if fake.calls != 1 {
		t.Errorf("GetStatus calls: want 1, got %d", fake.calls)
	}

	var body services.RegimeGateStatus
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Score != 62 {
		t.Errorf("score: want 62, got %d", body.Score)
	}
	if body.Tier != "NORMAL" {
		t.Errorf("tier: want NORMAL, got %q", body.Tier)
	}
	if body.SizingMultiplier != 0.8 {
		t.Errorf("sizing_multiplier: want 0.8, got %f", body.SizingMultiplier)
	}
}

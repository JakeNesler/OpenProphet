package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"prophet-trader/services"

	"github.com/gin-gonic/gin"
)

func setupPennyRouter(agg *services.PennySignalAggregator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	pc := NewPennyController(agg)
	r.GET("/api/v1/penny/candidates", pc.HandleGetCandidates)
	r.GET("/api/v1/penny/signal/:ticker", pc.HandleGetSignalDetail)
	r.GET("/api/v1/penny/universe", pc.HandleGetUniverse)
	r.POST("/api/v1/penny/scan", pc.HandleScanNow)
	r.DELETE("/api/v1/penny/blacklist", pc.HandleClearBlacklist)
	r.DELETE("/api/v1/penny/blacklist/:ticker", pc.HandleRemoveFromBlacklist)
	return r
}

// emptyAggregator creates an aggregator with zero-value sub-services.
// Safe because these tests only exercise the HTTP layer — aggregate() is never called,
// so the unexported maps and clients on the sub-services are never accessed.
func emptyAggregator() *services.PennySignalAggregator {
	return services.NewPennySignalAggregator(
		&services.PennyUniverseService{},
		&services.PennyScreenerService{},
		&services.SECEdgarService{},
		&services.SocialSignalService{},
	)
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	return body
}

func TestPennyController_GetCandidates_Empty(t *testing.T) {
	r := setupPennyRouter(emptyAggregator())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/candidates?min_score=60", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["count"].(float64) != 0 {
		t.Errorf("expected count=0, got %v", body["count"])
	}
}

// aggregatorWithSeededCandidate creates an aggregator and seeds one candidate
// with full context strings, for verifying summary/detail mode behavior.
func aggregatorWithSeededCandidate() *services.PennySignalAggregator {
	agg := emptyAggregator()
	services.SeedCandidateForTest(agg, services.CandidateScore{
		Ticker:           "ZZZZ",
		CompositeScore:   72,
		TechnicalScore:   30,
		RegulatoryScore:  22,
		SocialScore:      20,
		DominantSignal:   "technical",
		TechnicalContext: "RSI 72, volume 4.2x",
		RegulatoryEvent:  "8-K filed 09:32 ET",
		SocialContext:    "3.2x mention velocity, 71% bullish",
	})
	return agg
}

func TestPennyController_GetCandidates_DefaultStripsContext(t *testing.T) {
	r := setupPennyRouter(aggregatorWithSeededCandidate())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/candidates?min_score=60", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	candidates, _ := body["candidates"].([]interface{})
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0].(map[string]interface{})
	if c["ticker"] != "ZZZZ" || c["dominant_signal"] != "technical" {
		t.Errorf("scalar fields should be preserved, got %+v", c)
	}
	// Context fields use omitempty + are cleared in summary mode → should be absent from JSON
	if _, present := c["technical_context"]; present {
		t.Errorf("technical_context must be absent in default (summary) mode")
	}
	if _, present := c["regulatory_event"]; present {
		t.Errorf("regulatory_event must be absent in default (summary) mode")
	}
	if _, present := c["social_context"]; present {
		t.Errorf("social_context must be absent in default (summary) mode")
	}
}

func TestPennyController_GetCandidates_DetailIncludesContext(t *testing.T) {
	r := setupPennyRouter(aggregatorWithSeededCandidate())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/candidates?min_score=60&detail=true", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	c := body["candidates"].([]interface{})[0].(map[string]interface{})
	if c["technical_context"] != "RSI 72, volume 4.2x" {
		t.Errorf("detail=true should include technical_context, got %v", c["technical_context"])
	}
	if c["regulatory_event"] != "8-K filed 09:32 ET" {
		t.Errorf("detail=true should include regulatory_event, got %v", c["regulatory_event"])
	}
	if c["social_context"] != "3.2x mention velocity, 71% bullish" {
		t.Errorf("detail=true should include social_context, got %v", c["social_context"])
	}
}

func TestPennyController_GetSignalDetail_NotFound(t *testing.T) {
	r := setupPennyRouter(emptyAggregator())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/signal/NONE", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestPennyController_InvalidMinScore(t *testing.T) {
	r := setupPennyRouter(emptyAggregator())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/candidates?min_score=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPennyController_ScanNow(t *testing.T) {
	r := setupPennyRouter(emptyAggregator())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/penny/scan", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["status"] != "refreshing" {
		t.Errorf("expected status=refreshing, got %v", body["status"])
	}
}

func TestPennyController_GetUniverse_Empty(t *testing.T) {
	r := setupPennyRouter(emptyAggregator())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/penny/universe", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := parseBody(t, w)
	if body["count"].(float64) != 0 {
		t.Errorf("expected count=0, got %v", body["count"])
	}
}

func TestPennyController_ClearBlacklist_Returns200(t *testing.T) {
	agg := emptyAggregator()
	agg.AddToBlacklist("TICK", "bracket rejection test")
	r := setupPennyRouter(agg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/penny/blacklist", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if agg.IsBlacklisted("TICK") {
		t.Error("expected blacklist cleared after HandleClearBlacklist")
	}
}

func TestPennyController_RemoveFromBlacklist_Returns200(t *testing.T) {
	agg := emptyAggregator()
	agg.AddToBlacklist("RMVD", "test")
	agg.AddToBlacklist("KEEP", "test")
	r := setupPennyRouter(agg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/penny/blacklist/RMVD", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if agg.IsBlacklisted("RMVD") {
		t.Error("expected RMVD removed from blacklist")
	}
	if !agg.IsBlacklisted("KEEP") {
		t.Error("expected KEEP still blacklisted")
	}
}

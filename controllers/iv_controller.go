package controllers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"prophet-trader/services"
)

// IVController serves the generic, cross-strategy IV-rank endpoint used by
// Prophet's options-entry gate. Reuses HarvestIVRService (which now collects
// daily ATM IV for both Harvest and Prophet underlyings via the broadened
// collection loop in cmd/bot/main.go).
//
// When a RealizedVolService is wired in, the response is enriched with
// realized_vol_20d and iv_minus_rv — the premium-selling edge signal used by
// the harvest preflight and the Harvest entry rules.
type IVController struct {
	ivrSvc *services.HarvestIVRService
	rvSvc  *services.RealizedVolService // optional; nil disables enrichment
}

// NewIVController returns an IVController. rvSvc may be nil; the resulting
// IVRData will report RealizedVol20d=0, IVMinusRV=0 and downstream consumers
// must treat 0 as "no signal" (preflight and rules already do).
func NewIVController(ivrSvc *services.HarvestIVRService, rvSvc *services.RealizedVolService) *IVController {
	return &IVController{ivrSvc: ivrSvc, rvSvc: rvSvc}
}

// HandleGetIV serves GET /api/v1/iv/:symbol. Uses the most recent stored ATM
// IV snapshot as "current" so the LLM can read IV rank without first fetching
// a live options chain.
//
// Response shape matches services.IVRData. When the symbol has no history
// yet (e.g., a newly-added Prophet symbol that has not warmed up), IVR and
// IVPercentile come back as -1 and DaysOfHistory as 0 — the rules layer
// (TRADING_RULES_V2.md) treats DaysOfHistory < 20 as low-confidence.
func (c *IVController) HandleGetIV(ctx *gin.Context) {
	symbol := strings.ToUpper(strings.TrimSpace(ctx.Param("symbol")))
	if symbol == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "symbol is required"})
		return
	}
	data, err := c.ivrSvc.GetIVRDataLatest(symbol)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Optional RV enrichment. RV fetch failures are silently ignored so the
	// IVR response still lands; preflight + rules treat RealizedVol20d=0 as
	// "no signal" and fall through rather than incorrectly skipping.
	if c.rvSvc != nil && data.CurrentIV > 0 {
		if rv, rvErr := c.rvSvc.GetAnnualizedRealizedVol(ctx.Request.Context(), symbol, 20); rvErr == nil && rv > 0 {
			data.RealizedVol20d = rv
			data.IVMinusRV = data.CurrentIV - rv
		}
	}
	ctx.JSON(http.StatusOK, data)
}

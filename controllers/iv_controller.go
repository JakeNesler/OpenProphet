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
type IVController struct {
	ivrSvc *services.HarvestIVRService
}

// NewIVController returns an IVController.
func NewIVController(ivrSvc *services.HarvestIVRService) *IVController {
	return &IVController{ivrSvc: ivrSvc}
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
	ctx.JSON(http.StatusOK, data)
}

package controllers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"prophet-trader/services"
)

// IntradayController serves the intraday-signals endpoint used by Prophet's
// market-hours beats (auto-pushed into the prompt) and the get_intraday_signals
// MCP tool (on-demand reads of any symbol).
type IntradayController struct {
	svc *services.IntradaySignalService
}

// NewIntradayController returns an IntradayController.
func NewIntradayController(svc *services.IntradaySignalService) *IntradayController {
	return &IntradayController{svc: svc}
}

// HandleGetSignals serves GET /api/v1/intraday/signals?symbols=SPY,QQQ,NVDA
// Symbols are comma-separated, case-insensitive. Returns IntradaySignalSet
// (never errors at the HTTP layer; per-symbol fetch failures land in the
// Errors slice while other symbols remain populated).
func (c *IntradayController) HandleGetSignals(ctx *gin.Context) {
	raw := ctx.Query("symbols")
	if raw == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "symbols query param required (e.g., ?symbols=SPY,QQQ)"})
		return
	}
	parts := strings.Split(raw, ",")
	symbols := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.ToUpper(strings.TrimSpace(p))
		if s != "" {
			symbols = append(symbols, s)
		}
	}
	if len(symbols) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "no valid symbols in request"})
		return
	}

	set := c.svc.GetSignals(ctx.Request.Context(), symbols, time.Now().UTC())
	ctx.JSON(http.StatusOK, set)
}

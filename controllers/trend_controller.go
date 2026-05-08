package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"prophet-trader/services"
)

// TrendUniverse is the fixed set of ETFs TrendProphet trades. Requests for
// any other symbol return 400. The list mirrors TRADING_RULES_TREND.md.
var TrendUniverse = []string{"TLT", "GLD", "USO", "DBC", "UUP", "EEM"}

// TrendController exposes /api/v1/trend/* HTTP endpoints.
type TrendController struct {
	signalSvc *services.TrendSignalService
}

// NewTrendController constructs the controller.
func NewTrendController(signalSvc *services.TrendSignalService) *TrendController {
	return &TrendController{signalSvc: signalSvc}
}

// HandleGetSignal handles GET /api/v1/trend/signal/:symbol.
//
// Response codes:
//   200 → signal payload (see TrendSignal)
//   400 → symbol not in TrendUniverse
//   422 → bars exist but bars_count < 250 (insufficient history)
//   500 → upstream data fetch failed
func (tc *TrendController) HandleGetSignal(c *gin.Context) {
	symbol := strings.ToUpper(c.Param("symbol"))
	if !inTrendUniverse(symbol) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    fmt.Sprintf("symbol %s not in trend universe", symbol),
			"universe": TrendUniverse,
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	signal, err := tc.signalSvc.GetSignal(ctx, symbol)
	if errors.Is(err, services.ErrInsufficientHistory) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":            fmt.Sprintf("insufficient history for %s", symbol),
			"minimum_required": 250,
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, signal)
}

func inTrendUniverse(s string) bool {
	for _, t := range TrendUniverse {
		if t == s {
			return true
		}
	}
	return false
}

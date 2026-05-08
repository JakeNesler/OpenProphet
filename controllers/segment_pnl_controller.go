package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"prophet-trader/services"
)

// SegmentPnLController exposes /api/v1/segment-pnl/* HTTP endpoints.
type SegmentPnLController struct {
	service *services.SegmentPnLService
}

// NewSegmentPnLController constructs the controller.
func NewSegmentPnLController(service *services.SegmentPnLService) *SegmentPnLController {
	return &SegmentPnLController{service: service}
}

// HandleGetSegmentPnL handles GET /api/v1/segment-pnl/:strategy.
//
// Returns a JSON payload with unrealized P&L, deployed dollars, and percent
// of portfolio for the given strategy. Used by segment-scoped circuit
// breakers in the agent rules to determine whether the strategy has tripped
// its loss threshold.
func (sc *SegmentPnLController) HandleGetSegmentPnL(c *gin.Context) {
	strategy := c.Param("strategy")
	if strategy == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "strategy path parameter required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pnl, err := sc.service.GetSegmentPnL(ctx, strategy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pnl)
}
